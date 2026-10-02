//go:build integration

package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hylin/calendar/internal/model"
	"github.com/hylin/calendar/internal/repository"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTraceRepo_CRUD(t *testing.T) {
	truncateAll(t, testPool)
	ctx := context.Background()
	user := seedUser(t, testPool, "trace_crud")
	repo := repository.NewTraceRepository(testPool)
	cat, err := repository.NewCategoryRepository(testPool).Create(ctx, user.ID, model.CreateCategoryRequest{Name: "French", Color: "#0f766e"})
	require.NoError(t, err)

	tr, err := repo.Create(ctx, user.ID, model.CreateTraceRequest{CategoryID: &cat.ID, Day: "2026-09-30", Minutes: 15, Note: "podcast"})
	require.NoError(t, err)
	assert.Equal(t, model.Date("2026-09-30"), tr.Day)
	assert.Equal(t, 15, tr.Minutes)
	assert.Equal(t, &cat.ID, tr.CategoryID)

	got, err := repo.GetByID(ctx, tr.ID, user.ID)
	require.NoError(t, err)
	assert.Equal(t, "podcast", got.Note)

	mins, note, day := 20, "", model.Date("2026-09-29")
	upd, err := repo.Update(ctx, tr.ID, user.ID, model.UpdateTraceRequest{
		CategoryID: model.Optional[uuid.UUID]{Set: true}, Minutes: &mins, Note: &note, Day: &day,
	})
	require.NoError(t, err)
	assert.Nil(t, upd.CategoryID)
	assert.Equal(t, 20, upd.Minutes)
	assert.Equal(t, "", upd.Note)
	assert.Equal(t, day, upd.Day)

	require.NoError(t, repo.Delete(ctx, tr.ID, user.ID))
	assert.ErrorIs(t, repo.Delete(ctx, tr.ID, user.ID), pgx.ErrNoRows)
	_, err = repo.GetByID(ctx, tr.ID, user.ID)
	assert.ErrorIs(t, err, pgx.ErrNoRows)
}

func TestTraceRepo_ScopedToOwner(t *testing.T) {
	truncateAll(t, testPool)
	ctx := context.Background()
	owner := seedUser(t, testPool, "trace_owner")
	other := seedUser(t, testPool, "trace_other")
	repo := repository.NewTraceRepository(testPool)

	tr, err := repo.Create(ctx, owner.ID, model.CreateTraceRequest{Day: "2026-09-30", Minutes: 5})
	require.NoError(t, err)

	_, err = repo.GetByID(ctx, tr.ID, other.ID)
	assert.ErrorIs(t, err, pgx.ErrNoRows)
	mins := 50
	_, err = repo.Update(ctx, tr.ID, other.ID, model.UpdateTraceRequest{Minutes: &mins})
	assert.ErrorIs(t, err, pgx.ErrNoRows)
	assert.ErrorIs(t, repo.Delete(ctx, tr.ID, other.ID), pgx.ErrNoRows)

	list, err := repo.List(ctx, other.ID, "2026-09-01", "2026-09-30")
	require.NoError(t, err)
	assert.Empty(t, list)
}

func TestTraceRepo_ListRangeInclusive(t *testing.T) {
	truncateAll(t, testPool)
	ctx := context.Background()
	user := seedUser(t, testPool, "trace_list")
	repo := repository.NewTraceRepository(testPool)
	for _, d := range []model.Date{"2026-09-27", "2026-09-28", "2026-09-30", "2026-10-01"} {
		_, err := repo.Create(ctx, user.ID, model.CreateTraceRequest{Day: d, Minutes: 5})
		require.NoError(t, err)
	}
	list, err := repo.List(ctx, user.ID, "2026-09-28", "2026-09-30")
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, model.Date("2026-09-30"), list[0].Day) // newest day first
	assert.Equal(t, model.Date("2026-09-28"), list[1].Day)
}

func TestTraceRepo_CategoryDeleteAndUserCascade(t *testing.T) {
	truncateAll(t, testPool)
	ctx := context.Background()
	user := seedUser(t, testPool, "trace_cascade")
	repo := repository.NewTraceRepository(testPool)
	catRepo := repository.NewCategoryRepository(testPool)
	cat, err := catRepo.Create(ctx, user.ID, model.CreateCategoryRequest{Name: "Reading", Color: "#7c3aed"})
	require.NoError(t, err)
	tr, err := repo.Create(ctx, user.ID, model.CreateTraceRequest{CategoryID: &cat.ID, Day: "2026-09-30", Minutes: 10})
	require.NoError(t, err)

	// Deleting the Area leaves the trace, uncategorized.
	require.NoError(t, catRepo.Delete(ctx, cat.ID, user.ID))
	got, err := repo.GetByID(ctx, tr.ID, user.ID)
	require.NoError(t, err)
	assert.Nil(t, got.CategoryID)

	// Deleting the account deletes its traces.
	require.NoError(t, repository.NewUserRepository(testPool).Delete(ctx, user.ID))
	var n int
	require.NoError(t, testPool.QueryRow(ctx, `SELECT count(*) FROM time_traces`).Scan(&n))
	assert.Zero(t, n)
}

func TestEventStats_MergesTraces(t *testing.T) {
	truncateAll(t, testPool)
	ctx := context.Background()
	user := seedUser(t, testPool, "stats_traces")
	cal := seedDefaultCalendar(t, testPool, user.ID)
	catRepo := repository.NewCategoryRepository(testPool)
	eventRepo := repository.NewEventRepository(testPool)
	traceRepo := repository.NewTraceRepository(testPool)

	french, err := catRepo.Create(ctx, user.ID, model.CreateCategoryRequest{Name: "French", Color: "#0f766e", WeeklyTargetMinutes: 180})
	require.NoError(t, err)
	reading, err := catRepo.Create(ctx, user.ID, model.CreateCategoryRequest{Name: "Reading", Color: "#7c3aed"})
	require.NoError(t, err)

	start := time.Date(2024, 6, 11, 18, 0, 0, 0, time.UTC)
	_, err = eventRepo.Create(ctx, user.ID, cal.ID, model.CreateEventRequest{
		Title: "class", StartTime: start, EndTime: start.Add(30 * time.Minute), CategoryID: &french.ID,
	})
	require.NoError(t, err)
	mkTrace := func(catID *uuid.UUID, day model.Date, mins int, note string) {
		_, err := traceRepo.Create(ctx, user.ID, model.CreateTraceRequest{CategoryID: catID, Day: day, Minutes: mins, Note: note})
		require.NoError(t, err)
	}
	mkTrace(&french.ID, "2024-06-11", 10, "duolingo")
	mkTrace(&french.ID, "2024-06-12", 5, "")
	mkTrace(&reading.ID, "2024-06-12", 15, "paper") // an Area with traces only
	mkTrace(nil, "2024-06-13", 5, "")
	mkTrace(&french.ID, "2024-06-17", 60, "") // outside the window

	from := time.Date(2024, 6, 10, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 6, 17, 0, 0, 0, 0, time.UTC)
	stats, err := eventRepo.Stats(ctx, user.ID, from, to, "UTC")
	require.NoError(t, err)
	require.Len(t, stats.Areas, 3)

	fr := findArea(t, stats, "French")
	assert.Equal(t, 30, fr.EventMinutes)
	assert.Equal(t, 15, fr.TraceMinutes)
	assert.Equal(t, 45, fr.TotalMinutes)
	require.Len(t, fr.SubActivities, 1) // trace notes aren't sub-activities
	assert.Equal(t, "class", fr.SubActivities[0].Name)

	rd := findArea(t, stats, "Reading")
	assert.Equal(t, &reading.ID, rd.AreaID)
	assert.Equal(t, "#7c3aed", rd.AreaColor)
	assert.Equal(t, 0, rd.EventMinutes)
	assert.Equal(t, 15, rd.TotalMinutes)

	assert.Equal(t, 5, findArea(t, stats, "Uncategorized").TraceMinutes)
}

// A trace counts in the window holding the local midnight that starts its day.
func TestEventStats_TraceDayUsesTimeZone(t *testing.T) {
	truncateAll(t, testPool)
	ctx := context.Background()
	user := seedUser(t, testPool, "stats_tz")
	eventRepo := repository.NewEventRepository(testPool)
	_, err := repository.NewTraceRepository(testPool).Create(ctx, user.ID, model.CreateTraceRequest{Day: "2026-09-30", Minutes: 10})
	require.NoError(t, err)

	total := func(from, to time.Time, tz string) int {
		stats, err := eventRepo.Stats(ctx, user.ID, from, to, tz)
		require.NoError(t, err)
		n := 0
		for _, a := range stats.Areas {
			n += a.TraceMinutes
		}
		return n
	}
	// Toronto is UTC-4 in September: 2026-09-30 starts at 04:00Z there.
	end := time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)
	assert.Equal(t, 10, total(time.Date(2026, 9, 30, 4, 0, 0, 0, time.UTC), end, "America/Toronto"))
	assert.Equal(t, 0, total(time.Date(2026, 9, 30, 5, 0, 0, 0, time.UTC), end, "America/Toronto"))
	assert.Equal(t, 0, total(time.Date(2026, 9, 30, 4, 0, 0, 0, time.UTC), end, "UTC"))

	// "Elapsed so far" [day start, midday) includes the day; "the rest" [midday, …) doesn't.
	noon := time.Date(2026, 9, 30, 16, 0, 0, 0, time.UTC)
	assert.Equal(t, 10, total(time.Date(2026, 9, 28, 4, 0, 0, 0, time.UTC), noon, "America/Toronto"))
	assert.Equal(t, 0, total(noon, end, "America/Toronto"))
}
