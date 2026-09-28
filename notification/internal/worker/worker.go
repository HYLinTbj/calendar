package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"slices"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/hylin/calendar/internal/model"
	"github.com/hylin/calendar/internal/queue"
	"github.com/hylin/calendar/notification/internal/mailer"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type Worker struct {
	rdb     *redis.Client
	queue   *queue.ReminderQueue
	pool    *pgxpool.Pool
	mailer  *mailer.Mailer
	baseURL string
}

func New(rdb *redis.Client, pool *pgxpool.Pool, m *mailer.Mailer, baseURL string) *Worker {
	return &Worker{rdb: rdb, queue: queue.NewReminderQueue(rdb), pool: pool, mailer: m, baseURL: baseURL}
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
		if relayDown := w.handleReminder(ctx, member); relayDown {
			return // the rest would fail the same way; next poll
		}
	}
}

// maxAttempts bounds how often a reminder or invitation send is retried after
// transient failures before it's given up on.
const maxAttempts = 5

// startedGrace is how long after an event starts its reminders may still go out (a
// "0 minutes before" reminder is picked up by a poll shortly after the start).
const startedGrace = time.Minute

// handleReminder processes a single reminder. member is "<event_id>:<minutes>". It
// reports whether the mail relay itself failed (so the caller stops for this poll).
func (w *Worker) handleReminder(ctx context.Context, member string) (relayDown bool) {
	data, err := w.rdb.Get(ctx, "reminder:"+member).Result()
	if err == redis.Nil {
		w.rdb.ZRem(ctx, "reminders", member) // data gone (cancelled mid-poll): clear the entry
		return false
	}
	if err != nil {
		log.Printf("get reminder %s: %v", member, err)
		return false
	}
	done := func(why string) {
		if why != "" {
			log.Printf("dropped reminder %s: %s", member, why)
		}
		if err := w.queue.Done(ctx, member, data); err != nil {
			log.Printf("cleanup reminder %s: %v", member, err)
		}
	}
	var job queue.ReminderJob
	if err := json.Unmarshal([]byte(data), &job); err != nil {
		done(fmt.Sprintf("unreadable: %v", err))
		return false
	}

	// Send from the event as it is now. The job is a snapshot from when it was queued,
	// and not every way an event disappears cancels its reminders (deleting an account,
	// a series, a calendar's contents), so check it still exists and still wants this one.
	var title string
	var start time.Time
	var attendees []string
	var remindersRaw []byte
	err = w.pool.QueryRow(ctx,
		`SELECT title, start_time, attendees, reminders FROM events WHERE id = $1`, job.EventID,
	).Scan(&title, &start, &attendees, &remindersRaw)
	if err == pgx.ErrNoRows {
		done("event no longer exists")
		return false
	}
	if err != nil {
		log.Printf("load event for reminder %s: %v", member, err)
		return false
	}
	var reminders []model.Reminder
	_ = json.Unmarshal(remindersRaw, &reminders)
	if !slices.ContainsFunc(reminders, func(r model.Reminder) bool { return r.Minutes == job.Minutes }) {
		done("the event no longer has it")
		return false
	}
	if time.Now().After(start.Add(startedGrace)) {
		done("the event has already started") // e.g. after the service was down
		return false
	}

	// After a partial failure, only those not yet sent it.
	recipients := attendees
	if job.Pending != nil {
		recipients = slices.DeleteFunc(slices.Clone(job.Pending), func(a string) bool { return !slices.Contains(attendees, a) })
	}
	var sent, rejected int
	var retry []string
	for i, to := range recipients {
		err := w.mailer.SendReminder(to, title, start)
		switch {
		case err == nil:
			sent++
		case mailer.Systemic(err):
			// The relay is down or refusing us: keep the unsent ones for the next poll,
			// without counting it against them.
			log.Printf("reminder %s: mail relay unavailable, retrying next poll: %v", member, err)
			job.Pending = append(retry, recipients[i:]...)
			if err := w.queue.Retry(ctx, data, job, time.Now()); err != nil {
				log.Printf("requeue reminder %s: %v", member, err)
			}
			return true
		case mailer.Permanent(err):
			log.Printf("reminder %s to %s rejected, not retrying: %v", member, to, err)
			rejected++
		default:
			log.Printf("reminder %s to %s failed: %v", member, to, err)
			retry = append(retry, to)
		}
	}
	log.Printf("sent %s reminder -%dmin for event %s (%s) to %d of %d recipient(s)",
		job.Method, job.Minutes, job.EventID, title, sent, len(recipients))
	if len(retry) > 0 {
		at := time.Now().Add(time.Duration(job.Attempts+1) * time.Minute)
		if job.Attempts+1 < maxAttempts && at.Before(start.Add(startedGrace)) {
			job.Attempts++
			job.Pending = retry
			if err := w.queue.Retry(ctx, data, job, at); err != nil {
				log.Printf("requeue reminder %s: %v", member, err)
			}
			return false
		}
		log.Printf("giving up on reminder %s for %d recipient(s) after %d attempt(s)", member, len(retry), job.Attempts+1)
	}
	done("")
	return false
}

// processInvitations sends pending invitations, oldest first. A send that fails for
// that invitation is retried with backoff (1, 4, 9, 16 minutes) and marked 'failed'
// once it's rejected outright or has failed maxAttempts times, so undeliverable
// addresses can't hold up everyone else's. A failure of the relay itself ends the poll
// without counting against anyone. Invitations to events already over aren't sent.
func (w *Worker) processInvitations(ctx context.Context) {
	rows, err := w.pool.Query(ctx, `
		SELECT i.id, i.token, i.email, i.attempts, i.updated_at, e.title, e.location, e.start_time
		FROM event_invitations i
		JOIN events e ON e.id = i.event_id
		WHERE i.status = 'pending_send' AND e.end_time > NOW()
		  AND i.updated_at <= NOW() - i.attempts * i.attempts * interval '1 minute'
		ORDER BY i.updated_at
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
		attempts  int
		updatedAt time.Time
		title     string
		location  string
		startTime time.Time
	}
	var pending []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.token, &r.email, &r.attempts, &r.updatedAt, &r.title, &r.location, &r.startTime); err != nil {
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
			if mailer.Systemic(err) {
				log.Printf("mail relay unavailable, retrying invitations next poll: %v", err)
				return
			}
			status := "pending_send"
			if mailer.Permanent(err) || inv.attempts+1 >= maxAttempts {
				status = "failed"
			}
			log.Printf("send invitation %s (attempt %d, now %s): %v", inv.id, inv.attempts+1, status, err)
			if _, err := w.pool.Exec(ctx, `
				UPDATE event_invitations
				SET attempts = attempts + 1, last_error = $2, status = $3, updated_at = NOW()
				WHERE id = $1 AND status = 'pending_send'`,
				inv.id, err.Error(), status); err != nil {
				log.Printf("record invitation failure %s: %v", inv.id, err)
			}
			continue
		}
		// Only if nothing changed since it was read: an RSVP (status) or an edit to the
		// event (updated_at, bumped by ReInviteChanged) in the meantime must win.
		if _, err := w.pool.Exec(ctx, `
			UPDATE event_invitations SET status = 'sent', attempts = 0, last_error = NULL, updated_at = NOW()
			WHERE id = $1 AND status = 'pending_send' AND updated_at = $2`,
			inv.id, inv.updatedAt,
		); err != nil {
			log.Printf("mark invitation sent %s: %v", inv.id, err)
		}
		log.Printf("sent invitation to %s for event %q", inv.email, inv.title)
	}
}
