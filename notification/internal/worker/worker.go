package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/hylin/calendar/notification/internal/mailer"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type reminderJob struct {
	EventID   uuid.UUID `json:"event_id"`
	Minutes   int       `json:"minutes"`
	Method    string    `json:"method"`
	Title     string    `json:"title"`
	StartTime time.Time `json:"start_time"`
	Attendees []string  `json:"attendees"`
}

type Worker struct {
	rdb     *redis.Client
	pool    *pgxpool.Pool
	mailer  *mailer.Mailer
	baseURL string
}

func New(rdb *redis.Client, pool *pgxpool.Pool, m *mailer.Mailer, baseURL string) *Worker {
	return &Worker{rdb: rdb, pool: pool, mailer: m, baseURL: baseURL}
}

func (w *Worker) Run(ctx context.Context) {
	log.Println("notification worker started, polling every 30s")
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	w.process(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.process(ctx)
		}
	}
}

func (w *Worker) process(ctx context.Context) {
	w.processReminders(ctx)
	w.processInvitations(ctx)
}

func (w *Worker) processReminders(ctx context.Context) {
	now := strconv.FormatInt(time.Now().Unix(), 10)
	members, err := w.rdb.ZRangeByScore(ctx, "reminders", &redis.ZRangeBy{
		Min: "0",
		Max: now,
	}).Result()
	if err != nil {
		log.Printf("poll reminders: %v", err)
		return
	}
	for _, member := range members {
		w.handleReminder(ctx, member)
	}
}

// handleReminder processes a single reminder. member is "<event_id>:<minutes>".
func (w *Worker) handleReminder(ctx context.Context, member string) {
	data, err := w.rdb.Get(ctx, "reminder:"+member).Result()
	if err != nil {
		log.Printf("get reminder %s: %v", member, err)
		return
	}
	var job reminderJob
	if err := json.Unmarshal([]byte(data), &job); err != nil {
		log.Printf("unmarshal reminder %s: %v", member, err)
		return
	}
	if err := w.mailer.SendReminder(job.Title, job.StartTime, job.Attendees); err != nil {
		log.Printf("send reminder %s: %v", member, err)
		return
	}
	pipe := w.rdb.Pipeline()
	pipe.ZRem(ctx, "reminders", member)
	pipe.Del(ctx, "reminder:"+member)
	if _, err := pipe.Exec(ctx); err != nil {
		log.Printf("cleanup reminder %s: %v", member, err)
	}
	log.Printf("sent %s reminder -%dmin for event %s (%s)", job.Method, job.Minutes, job.EventID, job.Title)
}

func (w *Worker) processInvitations(ctx context.Context) {
	rows, err := w.pool.Query(ctx, `
		SELECT i.id, i.token, i.email, e.title, e.location, e.start_time
		FROM event_invitations i
		JOIN events e ON e.id = i.event_id
		WHERE i.status = 'pending_send'
		LIMIT 100`)
	if err != nil {
		log.Printf("poll invitations: %v", err)
		return
	}
	defer rows.Close()

	type row struct {
		id        uuid.UUID
		token     uuid.UUID
		email     string
		title     string
		location  string
		startTime time.Time
	}
	var pending []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.token, &r.email, &r.title, &r.location, &r.startTime); err != nil {
			log.Printf("scan invitation: %v", err)
			return
		}
		pending = append(pending, r)
	}
	if err := rows.Err(); err != nil {
		log.Printf("iterate invitations: %v", err)
		return
	}

	for _, inv := range pending {
		acceptURL := fmt.Sprintf("%s/invitations/%s/accept", w.baseURL, inv.token)
		declineURL := fmt.Sprintf("%s/invitations/%s/decline", w.baseURL, inv.token)

		if err := w.mailer.SendInvitation(inv.email, inv.title, inv.location, inv.startTime, acceptURL, declineURL); err != nil {
			log.Printf("send invitation %s: %v", inv.id, err)
			continue
		}
		if _, err := w.pool.Exec(ctx,
			`UPDATE event_invitations SET status='sent', updated_at=NOW() WHERE id=$1`, inv.id,
		); err != nil {
			log.Printf("mark invitation sent %s: %v", inv.id, err)
		}
		log.Printf("sent invitation to %s for event %q", inv.email, inv.title)
	}
}
