package queue_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/hylin/calendar/internal/queue"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestRedis(t *testing.T) *redis.Client {
	t.Helper()
	mr := miniredis.RunT(t)
	return redis.NewClient(&redis.Options{Addr: mr.Addr()})
}

func TestReminderQueue_Schedule_EnqueuesJobs(t *testing.T) {
	rdb := newTestRedis(t)
	q := queue.NewReminderQueue(rdb)
	ctx := context.Background()

	eventID := uuid.New()
	start := time.Now().Add(2 * time.Hour)
	jobs := []queue.ReminderJob{
		{EventID: eventID, Minutes: 15, Method: "email", Title: "Meeting", StartTime: start, Attendees: []string{"alice@example.com"}},
	}

	err := q.Schedule(ctx, jobs)
	require.NoError(t, err)

	// sorted set should have 1 member
	count, err := rdb.ZCard(ctx, "reminders").Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)

	// reminder data key should exist
	memberKey := eventID.String() + ":15"
	exists, err := rdb.Exists(ctx, "reminder:"+memberKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), exists)

	// meta key should exist
	exists, err = rdb.Exists(ctx, "reminder_meta:"+eventID.String()).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), exists)
}

func TestReminderQueue_Schedule_MultipleReminders(t *testing.T) {
	rdb := newTestRedis(t)
	q := queue.NewReminderQueue(rdb)
	ctx := context.Background()

	eventID := uuid.New()
	start := time.Now().Add(3 * time.Hour)
	jobs := []queue.ReminderJob{
		{EventID: eventID, Minutes: 15, Method: "email", Title: "Meeting", StartTime: start},
		{EventID: eventID, Minutes: 60, Method: "email", Title: "Meeting", StartTime: start},
	}

	err := q.Schedule(ctx, jobs)
	require.NoError(t, err)

	count, err := rdb.ZCard(ctx, "reminders").Result()
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)
}

func TestReminderQueue_Schedule_EmptyJobs_NoOp(t *testing.T) {
	rdb := newTestRedis(t)
	q := queue.NewReminderQueue(rdb)
	ctx := context.Background()

	err := q.Schedule(ctx, []queue.ReminderJob{})
	require.NoError(t, err)

	count, err := rdb.ZCard(ctx, "reminders").Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), count)
}

func TestReminderQueue_Cancel_RemovesAllKeys(t *testing.T) {
	rdb := newTestRedis(t)
	q := queue.NewReminderQueue(rdb)
	ctx := context.Background()

	eventID := uuid.New()
	start := time.Now().Add(2 * time.Hour)
	jobs := []queue.ReminderJob{
		{EventID: eventID, Minutes: 15, Method: "email", Title: "Meeting", StartTime: start},
		{EventID: eventID, Minutes: 30, Method: "email", Title: "Meeting", StartTime: start},
	}
	require.NoError(t, q.Schedule(ctx, jobs))

	err := q.Cancel(ctx, eventID)
	require.NoError(t, err)

	// sorted set should be empty
	count, err := rdb.ZCard(ctx, "reminders").Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), count)

	// data and meta keys should be gone
	exists, err := rdb.Exists(ctx, "reminder_meta:"+eventID.String()).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), exists)
}

func TestReminderQueue_Cancel_UnknownEvent_NoError(t *testing.T) {
	rdb := newTestRedis(t)
	q := queue.NewReminderQueue(rdb)
	ctx := context.Background()

	err := q.Cancel(ctx, uuid.New())
	assert.NoError(t, err)
}

func TestReminderQueue_Schedule_SetsCorrectScore(t *testing.T) {
	rdb := newTestRedis(t)
	q := queue.NewReminderQueue(rdb)
	ctx := context.Background()

	eventID := uuid.New()
	start := time.Now().UTC().Truncate(time.Minute).Add(48 * time.Hour) // future: past send times are skipped
	jobs := []queue.ReminderJob{
		{EventID: eventID, Minutes: 15, Method: "email", Title: "Meeting", StartTime: start},
	}
	require.NoError(t, q.Schedule(ctx, jobs))

	// Expected send time: start - 15min
	expected := start.Add(-15 * time.Minute).Unix()
	members, err := rdb.ZRangeWithScores(ctx, "reminders", 0, -1).Result()
	require.NoError(t, err)
	require.Len(t, members, 1)
	assert.InDelta(t, float64(expected), members[0].Score, 1.0)
}

func TestReminderQueue_Schedule_SkipsPastSendTimes(t *testing.T) {
	rdb := newTestRedis(t)
	q := queue.NewReminderQueue(rdb)
	ctx := context.Background()

	eventID := uuid.New()
	start := time.Now().Add(20 * time.Minute)
	require.NoError(t, q.Schedule(ctx, []queue.ReminderJob{
		{EventID: eventID, Minutes: 30, Method: "email", Title: "Soon", StartTime: start}, // due 10 min ago
		{EventID: eventID, Minutes: 10, Method: "email", Title: "Soon", StartTime: start}, // due in 10 min
	}))
	members, err := rdb.ZRange(ctx, "reminders", 0, -1).Result()
	require.NoError(t, err)
	assert.Equal(t, []string{eventID.String() + ":10"}, members, "a reminder whose time has passed isn't queued (it would fire at once)")

	// An event that already happened queues nothing, e.g. when it's edited afterwards.
	past := uuid.New()
	require.NoError(t, q.Schedule(ctx, []queue.ReminderJob{
		{EventID: past, Minutes: 10, Method: "email", Title: "Yesterday", StartTime: time.Now().Add(-24 * time.Hour)},
	}))
	n, err := rdb.Exists(ctx, "reminder_meta:"+past.String()).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), n)
}

func TestReminderQueue_RetryAndDone_LeaveARescheduledJobAlone(t *testing.T) {
	rdb := newTestRedis(t)
	q := queue.NewReminderQueue(rdb)
	ctx := context.Background()

	eventID := uuid.New()
	member := eventID.String() + ":10"
	job := queue.ReminderJob{EventID: eventID, Minutes: 10, Method: "email", Title: "Sync", StartTime: time.Now().Add(time.Hour)}
	require.NoError(t, q.Schedule(ctx, []queue.ReminderJob{job}))
	read, err := rdb.Get(ctx, "reminder:"+member).Result()
	require.NoError(t, err)

	// While the worker is sending, the event moves: the job is rescheduled under the same key.
	moved := job
	moved.StartTime = time.Now().Add(7 * 24 * time.Hour)
	require.NoError(t, q.Schedule(ctx, []queue.ReminderJob{moved}))
	current, err := rdb.Get(ctx, "reminder:"+member).Result()
	require.NoError(t, err)

	// The worker's retry and done both see a changed job and leave it alone.
	retried := job
	retried.Attempts, retried.Pending = 1, []string{"r2@example.com"}
	require.NoError(t, q.Retry(ctx, read, retried, time.Now().Add(time.Minute)))
	require.NoError(t, q.Done(ctx, member, read))
	after, err := rdb.Get(ctx, "reminder:"+member).Result()
	require.NoError(t, err)
	assert.Equal(t, current, after, "the rescheduled job survives")
	score, err := rdb.ZScore(ctx, "reminders", member).Result()
	require.NoError(t, err)
	assert.InDelta(t, float64(moved.StartTime.Add(-10*time.Minute).Unix()), score, 1)

	// Unchanged, they apply.
	require.NoError(t, q.Retry(ctx, current, retried, time.Now().Add(time.Minute)))
	requeued, err := rdb.Get(ctx, "reminder:"+member).Result()
	require.NoError(t, err)
	assert.Contains(t, requeued, `"attempts":1`)
	require.NoError(t, q.Done(ctx, member, requeued))
	n, err := rdb.Exists(ctx, "reminder:"+member).Result()
	require.NoError(t, err)
	assert.Zero(t, n)
	card, err := rdb.ZCard(ctx, "reminders").Result()
	require.NoError(t, err)
	assert.Zero(t, card)
}
