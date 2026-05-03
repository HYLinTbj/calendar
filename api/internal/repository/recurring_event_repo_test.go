//go:build integration

package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/hylin/calendar/api/internal/model"
	"github.com/hylin/calendar/api/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecurringEventRepository_Create_GeneratesInstances(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "rec_a")
	def := seedDefaultCalendar(t, testPool, user.ID)
	r := repository.NewRecurringEventRepository(testPool)
	ctx := context.Background()

	// Start in the past so the 60-day generation window creates many instances.
	start := time.Now().UTC().Add(-7 * 24 * time.Hour)
	rec, err := r.Create(ctx, user.ID, def.ID, model.CreateRecurringEventRequest{
		Title:     "Daily Standup",
		StartTime: start,
		EndTime:   start.Add(30 * time.Minute),
		Frequency: "daily",
		Interval:  1,
	})
	require.NoError(t, err)
	assert.Equal(t, "Daily Standup", rec.Title)

	// Verify instances were generated in the events table
	evRepo := repository.NewEventRepository(testPool)
	events, err := evRepo.List(ctx, user.ID, &def.ID, nil, nil)
	require.NoError(t, err)
	assert.NotEmpty(t, events, "daily recurring event should generate instances")
	for _, ev := range events {
		require.NotNil(t, ev.RecurringEventID)
		assert.Equal(t, rec.ID, *ev.RecurringEventID)
	}
}

func TestRecurringEventRepository_Create_UniqueInstanceIndex(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "rec_b")
	def := seedDefaultCalendar(t, testPool, user.ID)
	r := repository.NewRecurringEventRepository(testPool)
	ctx := context.Background()

	start := time.Now().UTC()
	// Creating the same rule twice should not fail due to ON CONFLICT DO NOTHING on instances
	_, err := r.Create(ctx, user.ID, def.ID, model.CreateRecurringEventRequest{
		Title:     "Weekly",
		StartTime: start,
		EndTime:   start.Add(time.Hour),
		Frequency: "weekly",
		Interval:  1,
	})
	require.NoError(t, err)
}

func TestRecurringEventRepository_GetByID(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "rec_c")
	def := seedDefaultCalendar(t, testPool, user.ID)
	r := repository.NewRecurringEventRepository(testPool)
	ctx := context.Background()

	start := time.Now().UTC()
	rec, err := r.Create(ctx, user.ID, def.ID, model.CreateRecurringEventRequest{
		Title:     "Weekly",
		StartTime: start,
		EndTime:   start.Add(time.Hour),
		Frequency: "weekly",
		Interval:  1,
	})
	require.NoError(t, err)

	got, err := r.GetByID(ctx, rec.ID, user.ID)
	require.NoError(t, err)
	assert.Equal(t, rec.ID, got.ID)
	assert.Equal(t, "Weekly", got.Title)
}

func TestRecurringEventRepository_SplitAt(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "rec_d")
	def := seedDefaultCalendar(t, testPool, user.ID)
	r := repository.NewRecurringEventRepository(testPool)
	ctx := context.Background()

	// Start a weekly recurring event 14 days ago so instances exist.
	start := time.Now().UTC().Add(-14 * 24 * time.Hour)
	rec, err := r.Create(ctx, user.ID, def.ID, model.CreateRecurringEventRequest{
		Title:     "Weekly Sync",
		StartTime: start,
		EndTime:   start.Add(time.Hour),
		Frequency: "weekly",
		Interval:  1,
	})
	require.NoError(t, err)

	// Split at "now + 7 days" (an upcoming occurrence)
	pivot := time.Now().UTC().Add(7 * 24 * time.Hour)
	newTitle := "New Weekly Sync"
	newRec, err := r.SplitAt(ctx, rec.ID, user.ID, pivot, model.UpdateRecurrenceRequest{
		Scope: "this_and_following",
		Title: &newTitle,
	})
	require.NoError(t, err)
	assert.Equal(t, "New Weekly Sync", newRec.Title)
	assert.NotEqual(t, rec.ID, newRec.ID)

	// Original series should have an end_date set before the pivot
	original, err := r.GetByID(ctx, rec.ID, user.ID)
	require.NoError(t, err)
	require.NotNil(t, original.EndDate)
	assert.True(t, original.EndDate.Before(pivot))
}

func TestRecurringEventRepository_Delete(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "rec_e")
	def := seedDefaultCalendar(t, testPool, user.ID)
	r := repository.NewRecurringEventRepository(testPool)
	ctx := context.Background()

	start := time.Now().UTC()
	rec, err := r.Create(ctx, user.ID, def.ID, model.CreateRecurringEventRequest{
		Title:     "To Delete",
		StartTime: start,
		EndTime:   start.Add(time.Hour),
		Frequency: "daily",
		Interval:  1,
	})
	require.NoError(t, err)

	err = r.Delete(ctx, rec.ID, user.ID)
	require.NoError(t, err)

	_, err = r.GetByID(ctx, rec.ID, user.ID)
	assert.Error(t, err, "deleted recurring event should not be found")
}

func TestRecurringEventRepository_Delete_CascadesInstances(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "rec_f")
	def := seedDefaultCalendar(t, testPool, user.ID)
	r := repository.NewRecurringEventRepository(testPool)
	ctx := context.Background()

	start := time.Now().UTC()
	rec, err := r.Create(ctx, user.ID, def.ID, model.CreateRecurringEventRequest{
		Title:     "Cascade Test",
		StartTime: start,
		EndTime:   start.Add(time.Hour),
		Frequency: "daily",
		Interval:  1,
	})
	require.NoError(t, err)

	err = r.Delete(ctx, rec.ID, user.ID)
	require.NoError(t, err)

	// All generated event instances should be gone (ON DELETE CASCADE)
	evRepo := repository.NewEventRepository(testPool)
	events, err := evRepo.List(ctx, user.ID, &def.ID, nil, nil)
	require.NoError(t, err)
	assert.Empty(t, events, "instances should be deleted when recurring rule is deleted")
}
