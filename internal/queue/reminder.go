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
	// Attempts and Pending track a send that failed for some recipients: how many tries
	// so far, and who still hasn't been sent it (nil = everyone).
	Attempts int      `json:"attempts,omitempty"`
	Pending  []string `json:"pending,omitempty"`
}

type ReminderQueue struct {
	rdb *redis.Client
}

func NewReminderQueue(rdb *redis.Client) *ReminderQueue {
	return &ReminderQueue{rdb: rdb}
}

// Schedule enqueues one reminder per job, keyed by <event_id>:<minutes>.
// A meta key tracks all minute-offsets so Cancel can clean them up efficiently.
// Reminders whose send time has already passed are skipped: every edit reschedules an
// event's reminders, and re-queuing a past one would send it again straight away.
func (q *ReminderQueue) Schedule(ctx context.Context, jobs []ReminderJob) error {
	now := time.Now()
	var due []ReminderJob
	for _, job := range jobs {
		if job.StartTime.Add(-time.Duration(job.Minutes) * time.Minute).After(now) {
			due = append(due, job)
		}
	}
	if len(due) == 0 {
		return nil
	}
	eventID := due[0].EventID
	var minutesList []int
	// Atomic, so the worker never sees a queue entry without its payload (see ClearOrphan).
	pipe := q.rdb.TxPipeline()
	for _, job := range due {
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

// Cancel removes the given event's reminders that haven't come due yet from the queue.
// Due ones are left to the worker, which sends each from the event as it is now, or
// drops it if the event or that reminder is gone: Schedule skips past send times, so
// one cancelled by an edit before the worker got to it would never be sent.
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
	now := time.Now()
	pipe := q.rdb.Pipeline()
	for _, m := range minutesList {
		member := memberKey(eventID, m)
		var job ReminderJob
		if data, err := q.rdb.Get(ctx, "reminder:"+member).Bytes(); err == nil && json.Unmarshal(data, &job) == nil &&
			!job.StartTime.Add(-time.Duration(job.Minutes)*time.Minute).After(now) {
			continue
		}
		pipe.ZRem(ctx, "reminders", member)
		pipe.Del(ctx, "reminder:"+member)
	}
	pipe.Del(ctx, metaKey)
	_, err = pipe.Exec(ctx)
	return err
}

// The worker finishes or re-queues a job only if it's unchanged since the worker read
// it (prev): an edit to the event meanwhile reschedules it under the same key, and that
// newer job must not be overwritten or deleted.
var (
	retryScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  redis.call('SET', KEYS[1], ARGV[2])
  redis.call('ZADD', KEYS[2], ARGV[3], ARGV[4])
  return 1
end
return 0`)
	doneScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  redis.call('DEL', KEYS[1])
  redis.call('ZREM', KEYS[2], ARGV[2])
  return 1
end
return 0`)
	orphanScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 0 then
  redis.call('ZREM', KEYS[2], ARGV[1])
  return 1
end
return 0`)
)

// ClearOrphan removes member, whose payload the worker found gone (it was cancelled),
// from the queue — unless it has been scheduled again since.
func (q *ReminderQueue) ClearOrphan(ctx context.Context, member string) error {
	return orphanScript.Run(ctx, q.rdb, []string{"reminder:" + member, "reminders"}, member).Err()
}

// Retry stores job's updated state (Attempts, Pending) and re-queues it to be sent at
// at, unless the job has changed since it was read as prev.
func (q *ReminderQueue) Retry(ctx context.Context, prev string, job ReminderJob, at time.Time) error {
	member := memberKey(job.EventID, job.Minutes)
	data, err := json.Marshal(job)
	if err != nil {
		return err
	}
	return retryScript.Run(ctx, q.rdb, []string{"reminder:" + member, "reminders"},
		prev, string(data), at.Unix(), member).Err()
}

// Done removes a sent (or abandoned) job, unless it has changed since it was read as prev.
func (q *ReminderQueue) Done(ctx context.Context, member, prev string) error {
	return doneScript.Run(ctx, q.rdb, []string{"reminder:" + member, "reminders"}, prev, member).Err()
}

func memberKey(eventID uuid.UUID, minutes int) string {
	return eventID.String() + ":" + strconv.Itoa(minutes)
}
