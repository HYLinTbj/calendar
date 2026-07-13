//go:build integration

package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hylin/calendar/internal/model"
	"github.com/hylin/calendar/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// findArea returns the AreaStat with the given name, or fails the test.
func findArea(t *testing.T, stats *model.TimeStats, name string) model.AreaStat {
	t.Helper()
	for _, a := range stats.Areas {
		if a.AreaName == name {
			return a
		}
	}
	t.Fatalf("area %q not found in stats %+v", name, stats.Areas)
	return model.AreaStat{}
}

func TestEventStats_AggregatesByAreaAndTitle(t *testing.T) {
	truncateAll(t, testPool)
	ctx := context.Background()

	user := seedUser(t, testPool, "stats_a")
	cal := seedDefaultCalendar(t, testPool, user.ID)
	catRepo := repository.NewCategoryRepository(testPool)
	eventRepo := repository.NewEventRepository(testPool)

	french, err := catRepo.Create(ctx, user.ID, model.CreateCategoryRequest{
		Name: "French", Color: "#4285F4", WeeklyTargetMinutes: 300,
	})
	require.NoError(t, err)
	gym, err := catRepo.Create(ctx, user.ID, model.CreateCategoryRequest{
		Name: "Gym", Color: "#34A853",
	})
	require.NoError(t, err)

	// mkEvent creates an event with explicit category, title, duration and all-day flag.
	mkEvent := func(catID *uuid.UUID, title string, day, startHour, durMin int, allDay bool) {
		start := time.Date(2024, 6, day, startHour, 0, 0, 0, time.UTC)
		_, err := eventRepo.Create(ctx, user.ID, cal.ID, model.CreateEventRequest{
			Title:      title,
			StartTime:  start,
			EndTime:    start.Add(time.Duration(durMin) * time.Minute),
			CategoryID: catID,
			AllDay:     allDay,
		})
		require.NoError(t, err)
	}

	// Inside the query window [2024-06-10, 2024-06-17):
	mkEvent(&french.ID, "reading", 10, 9, 60, false)    // French reading 60
	mkEvent(&french.ID, "listening", 11, 10, 30, false) // French listening 30
	mkEvent(&french.ID, "reading", 12, 14, 45, false)   // French reading +45 -> reading 105
	mkEvent(&gym.ID, "weights", 13, 18, 90, false)      // Gym weights 90
	mkEvent(nil, "Errand", 14, 12, 20, false)           // no category -> Uncategorized 20

	// Excluded: all-day event carries no meaningful duration.
	mkEvent(&french.ID, "immersion day", 15, 0, 1440, true)
	// Excluded: outside the window.
	mkEvent(&french.ID, "reading", 20, 9, 60, false)

	from := time.Date(2024, 6, 10, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 6, 17, 0, 0, 0, 0, time.UTC)
	stats, err := eventRepo.Stats(ctx, user.ID, from, to)
	require.NoError(t, err)

	require.Len(t, stats.Areas, 3) // French, Gym, Uncategorized

	fr := findArea(t, stats, "French")
	assert.Equal(t, &french.ID, fr.AreaID)
	assert.Equal(t, 300, fr.WeeklyTargetMinutes)
	assert.Equal(t, 135, fr.TotalMinutes) // reading 105 + listening 30
	require.Len(t, fr.SubActivities, 2)
	subs := map[string]int{}
	for _, s := range fr.SubActivities {
		subs[s.Name] = s.Minutes
	}
	assert.Equal(t, 105, subs["reading"])
	assert.Equal(t, 30, subs["listening"])

	assert.Equal(t, 90, findArea(t, stats, "Gym").TotalMinutes)

	uncat := findArea(t, stats, "Uncategorized")
	assert.Nil(t, uncat.AreaID)
	assert.Equal(t, 20, uncat.TotalMinutes)
}

func TestEventStats_ScopedToOwner(t *testing.T) {
	truncateAll(t, testPool)
	ctx := context.Background()

	owner := seedUser(t, testPool, "stats_owner")
	other := seedUser(t, testPool, "stats_other")
	ownerCal := seedDefaultCalendar(t, testPool, owner.ID)
	otherCal := seedDefaultCalendar(t, testPool, other.ID)
	eventRepo := repository.NewEventRepository(testPool)

	mk := func(ownerID, calID uuid.UUID) {
		start := time.Date(2024, 6, 12, 9, 0, 0, 0, time.UTC)
		_, err := eventRepo.Create(ctx, ownerID, calID, model.CreateEventRequest{
			Title: "work", StartTime: start, EndTime: start.Add(time.Hour),
		})
		require.NoError(t, err)
	}
	mk(owner.ID, ownerCal.ID)
	mk(other.ID, otherCal.ID)

	from := time.Date(2024, 6, 10, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 6, 17, 0, 0, 0, 0, time.UTC)
	stats, err := eventRepo.Stats(ctx, owner.ID, from, to)
	require.NoError(t, err)

	total := 0
	for _, a := range stats.Areas {
		total += a.TotalMinutes
	}
	assert.Equal(t, 60, total) // only the owner's single hour
}
