package queue

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type ReminderJob struct {
	EventID   uuid.UUID `json:"event_id"`
	Minutes   int       `json:"minutes"`
	Method    string    `json:"method"` // "email" | "notification"
	Title     string    `json:"title"`
	StartTime time.Time `json:"start_time"`
	Attendees []string  `json:"attendees"`
}

type ReminderQueue struct {
	rdb *redis.Client
}

func NewReminderQueue(rdb *redis.Client) *ReminderQueue {
	return &ReminderQueue{rdb: rdb}
}

// Schedule enqueues one reminder per job, keyed by <event_id>:<minutes>.
// A meta key tracks all minute-offsets so Cancel can clean them up efficiently.
func (q *ReminderQueue) Schedule(ctx context.Context, jobs []ReminderJob) error {
	if len(jobs) == 0 {
		return nil
	}
	eventID := jobs[0].EventID
	var minutesList []int
	pipe := q.rdb.Pipeline()
	for _, job := range jobs {
		sendAt := job.StartTime.Add(-time.Duration(job.Minutes) * time.Minute)
		member := memberKey(job.EventID, job.Minutes)
		data, err := json.Marshal(job)
		if err != nil {
			return err
		}
		pipe.ZAdd(ctx, "reminders", redis.Z{Score: float64(sendAt.Unix()), Member: member})
		pipe.Set(ctx, "reminder:"+member, data, 0)
		minutesList = append(minutesList, job.Minutes)
	}
	metaData, _ := json.Marshal(minutesList)
	pipe.Set(ctx, "reminder_meta:"+eventID.String(), metaData, 0)
	_, err := pipe.Exec(ctx)
	return err
}

// Cancel removes all reminders for the given event from the queue.
func (q *ReminderQueue) Cancel(ctx context.Context, eventID uuid.UUID) error {
	metaKey := "reminder_meta:" + eventID.String()
	metaData, err := q.rdb.Get(ctx, metaKey).Bytes()
	if err == redis.Nil {
		return nil
	}
	if err != nil {
		return err
	}
	var minutesList []int
	if err := json.Unmarshal(metaData, &minutesList); err != nil {
		return err
	}
	pipe := q.rdb.Pipeline()
	for _, m := range minutesList {
		member := memberKey(eventID, m)
		pipe.ZRem(ctx, "reminders", member)
		pipe.Del(ctx, "reminder:"+member)
	}
	pipe.Del(ctx, metaKey)
	_, err = pipe.Exec(ctx)
	return err
}

func memberKey(eventID uuid.UUID, minutes int) string {
	return eventID.String() + ":" + strconv.Itoa(minutes)
}
