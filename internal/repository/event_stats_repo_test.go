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
	stats, err := eventRepo.Stats(ctx, user.ID, from, to, "UTC")
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
	stats, err := eventRepo.Stats(ctx, owner.ID, from, to, "UTC")
	require.NoError(t, err)

	total := 0
	for _, a := range stats.Areas {
		total += a.TotalMinutes
	}
	assert.Equal(t, 60, total) // only the owner's single hour
}

// weeklyArea returns the AreaWeekly with the given name, or fails the test.
func weeklyArea(t *testing.T, stats *model.WeeklyStats, name string) model.AreaWeekly {
	t.Helper()
	for _, a := range stats.Areas {
		if a.AreaName == name {
			return a
		}
	}
	t.Fatalf("area %q not found in weekly stats %+v", name, stats.Areas)
	return model.AreaWeekly{}
}

func TestWeeklyStats_BucketsEventsAndTraces(t *testing.T) {
	truncateAll(t, testPool)
	ctx := context.Background()
	user := seedUser(t, testPool, "weekly")
	other := seedUser(t, testPool, "weekly_other")
	cal := seedDefaultCalendar(t, testPool, user.ID)
	otherCal := seedDefaultCalendar(t, testPool, other.ID)
	catRepo := repository.NewCategoryRepository(testPool)
	eventRepo := repository.NewEventRepository(testPool)
	traceRepo := repository.NewTraceRepository(testPool)

	french, err := catRepo.Create(ctx, user.ID, model.CreateCategoryRequest{Name: "French", Color: "#0f766e", WeeklyTargetMinutes: 120})
	require.NoError(t, err)
	gym, err := catRepo.Create(ctx, user.ID, model.CreateCategoryRequest{Name: "Gym", Color: "#34A853"})
	require.NoError(t, err)
	_, err = catRepo.Create(ctx, user.ID, model.CreateCategoryRequest{Name: "Reading", Color: "#7c3aed"}) // no time: not listed
	require.NoError(t, err)

	mkEvent := func(ownerID, calID uuid.UUID, catID *uuid.UUID, title string, start time.Time, mins int, allDay bool) {
		_, err := eventRepo.Create(ctx, ownerID, calID, model.CreateEventRequest{
			Title: title, StartTime: start, EndTime: start.Add(time.Duration(mins) * time.Minute), CategoryID: catID, AllDay: allDay,
		})
		require.NoError(t, err)
	}
	mkTrace := func(catID *uuid.UUID, day model.Date, mins int) {
		_, err := traceRepo.Create(ctx, user.ID, model.CreateTraceRequest{CategoryID: catID, Day: day, Minutes: mins})
		require.NoError(t, err)
	}
	at := func(day, hour int) time.Time { return time.Date(2024, 6, day, hour, 0, 0, 0, time.UTC) }

	// Weeks starting Monday 2024-06-03, 06-10 and 06-17.
	mkEvent(user.ID, cal.ID, &french.ID, "reading", at(4, 9), 60, false)
	mkTrace(nil, "2024-06-05", 10)
	mkEvent(user.ID, cal.ID, &french.ID, "reading", at(11, 9), 20, false)
	mkEvent(user.ID, cal.ID, &french.ID, "listening", at(13, 9), 10, false)
	mkTrace(&french.ID, "2024-06-12", 15)
	mkEvent(user.ID, cal.ID, &gym.ID, "weights", at(18, 18), 45, false)
	// Not counted: all-day, after the range, another user's.
	mkEvent(user.ID, cal.ID, &french.ID, "immersion", at(19, 0), 1440, true)
	mkEvent(user.ID, cal.ID, &french.ID, "reading", at(24, 9), 60, false)
	mkTrace(&french.ID, "2024-06-24", 30)
	mkEvent(other.ID, otherCal.ID, nil, "work", at(12, 9), 60, false)

	from, to := at(3, 0), at(24, 0)
	stats, err := eventRepo.WeeklyStats(ctx, user.ID, from, to, "UTC", 1)
	require.NoError(t, err)
	assert.Equal(t, []model.Date{"2024-06-03", "2024-06-10", "2024-06-17"}, stats.Weeks)
	assert.Equal(t, 1, stats.WeekStart)
	assert.Equal(t, "UTC", stats.TZ)

	// Name order, Uncategorized last; Areas with no time in the range aren't listed.
	names := []string{}
	for _, a := range stats.Areas {
		names = append(names, a.AreaName)
	}
	assert.Equal(t, []string{"French", "Gym", "Uncategorized"}, names)

	fr := weeklyArea(t, stats, "French")
	assert.Equal(t, &french.ID, fr.AreaID)
	assert.Equal(t, 120, fr.WeeklyTargetMinutes)
	assert.Equal(t, []int{60, 45, 0}, fr.Minutes) // 20 + 10 of events, 15 of a trace
	assert.Equal(t, []int{0, 0, 45}, weeklyArea(t, stats, "Gym").Minutes)
	assert.Equal(t, []int{10, 0, 0}, weeklyArea(t, stats, "Uncategorized").Minutes)

	// A week's minutes are what Stats gives over that week.
	week, err := eventRepo.Stats(ctx, user.ID, at(10, 0), at(17, 0), "UTC")
	require.NoError(t, err)
	assert.Equal(t, fr.Minutes[1], findArea(t, week, "French").TotalMinutes)

	// A range ending mid-week ends with that partial week.
	stats, err = eventRepo.WeeklyStats(ctx, user.ID, from, at(12, 0), "UTC", 1)
	require.NoError(t, err)
	assert.Equal(t, []model.Date{"2024-06-03", "2024-06-10"}, stats.Weeks)
	assert.Equal(t, []int{60, 20}, weeklyArea(t, stats, "French").Minutes) // not yet the trace on the 12th

	_, err = eventRepo.WeeklyStats(ctx, user.ID, from, to, "Not/AZone", 1)
	assert.ErrorIs(t, err, repository.ErrInvalidTimeZone)
}

func TestWeeklyStats_WeekStart(t *testing.T) {
	truncateAll(t, testPool)
	ctx := context.Background()
	user := seedUser(t, testPool, "weekly_ws")
	cal := seedDefaultCalendar(t, testPool, user.ID)
	eventRepo := repository.NewEventRepository(testPool)

	start := time.Date(2024, 6, 9, 10, 0, 0, 0, time.UTC) // a Sunday
	_, err := eventRepo.Create(ctx, user.ID, cal.ID, model.CreateEventRequest{Title: "hike", StartTime: start, EndTime: start.Add(2 * time.Hour)})
	require.NoError(t, err)

	from := time.Date(2024, 6, 2, 0, 0, 0, 0, time.UTC) // a Sunday
	to := time.Date(2024, 6, 16, 0, 0, 0, 0, time.UTC)

	// Sunday weeks: the hike opens the second one.
	stats, err := eventRepo.WeeklyStats(ctx, user.ID, from, to, "UTC", 0)
	require.NoError(t, err)
	assert.Equal(t, []model.Date{"2024-06-02", "2024-06-09"}, stats.Weeks)
	assert.Equal(t, []int{0, 120}, weeklyArea(t, stats, "Uncategorized").Minutes)

	// Monday weeks: it closes the week of the 3rd, and a range from a Sunday starts
	// with the week holding it.
	stats, err = eventRepo.WeeklyStats(ctx, user.ID, from, to, "UTC", 1)
	require.NoError(t, err)
	assert.Equal(t, []model.Date{"2024-05-27", "2024-06-03", "2024-06-10"}, stats.Weeks)
	assert.Equal(t, []int{0, 120, 0}, weeklyArea(t, stats, "Uncategorized").Minutes)
}

// Weeks are of local days in tz, also across a DST change.
func TestWeeklyStats_TimeZone(t *testing.T) {
	truncateAll(t, testPool)
	ctx := context.Background()
	user := seedUser(t, testPool, "weekly_tz")
	cal := seedDefaultCalendar(t, testPool, user.ID)
	eventRepo := repository.NewEventRepository(testPool)
	traceRepo := repository.NewTraceRepository(testPool)

	mkEvent := func(start time.Time, mins int) {
		_, err := eventRepo.Create(ctx, user.ID, cal.ID, model.CreateEventRequest{Title: "work", StartTime: start, EndTime: start.Add(time.Duration(mins) * time.Minute)})
		require.NoError(t, err)
	}
	mkTrace := func(day model.Date, mins int) {
		_, err := traceRepo.Create(ctx, user.ID, model.CreateTraceRequest{Day: day, Minutes: mins})
		require.NoError(t, err)
	}
	weekly := func(from, to time.Time, tz string) *model.WeeklyStats {
		stats, err := eventRepo.WeeklyStats(ctx, user.ID, from, to, tz, 0)
		require.NoError(t, err)
		return stats
	}

	// Saturday 2026-10-03 23:30 in Toronto (UTC-4) is already Sunday in UTC.
	mkEvent(time.Date(2026, 10, 4, 3, 30, 0, 0, time.UTC), 30)
	mkTrace("2026-10-04", 10)
	from := time.Date(2026, 9, 27, 4, 0, 0, 0, time.UTC) // Sunday midnight in Toronto
	to := time.Date(2026, 10, 11, 4, 0, 0, 0, time.UTC)
	stats := weekly(from, to, "America/Toronto")
	assert.Equal(t, []model.Date{"2026-09-27", "2026-10-04"}, stats.Weeks)
	assert.Equal(t, []int{30, 10}, weeklyArea(t, stats, "Uncategorized").Minutes)

	stats = weekly(time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC), "UTC")
	assert.Equal(t, []int{0, 40}, weeklyArea(t, stats, "Uncategorized").Minutes)

	// Toronto leaves DST on Sunday 2026-11-01, so its week runs 04:00Z to 05:00Z.
	mkEvent(time.Date(2026, 11, 1, 3, 30, 0, 0, time.UTC), 20) // Saturday 23:30 EDT
	mkTrace("2026-11-01", 5)
	mkEvent(time.Date(2026, 11, 8, 4, 30, 0, 0, time.UTC), 15) // Saturday 23:30 EST
	stats = weekly(time.Date(2026, 10, 25, 4, 0, 0, 0, time.UTC), time.Date(2026, 11, 8, 5, 0, 0, 0, time.UTC), "America/Toronto")
	assert.Equal(t, []model.Date{"2026-10-25", "2026-11-01"}, stats.Weeks)
	assert.Equal(t, []int{20, 20}, weeklyArea(t, stats, "Uncategorized").Minutes)
}
