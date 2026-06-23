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
	start := time.Date(2024, 6, 15, 10, 0, 0, 0, time.UTC)
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
