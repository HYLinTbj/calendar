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

func TestRecurringEventRepository_Create_RespectsMaxOccurrences(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "rec_maxocc")
	def := seedDefaultCalendar(t, testPool, user.ID)
	r := repository.NewRecurringEventRepository(testPool)
	ctx := context.Background()

	// Start well in the past so the 60-day window would otherwise generate far more than 3.
	start := time.Now().UTC().Add(-30 * 24 * time.Hour)
	rec, err := r.Create(ctx, user.ID, def.ID, model.CreateRecurringEventRequest{
		Title:          "Capped Daily",
		StartTime:      start,
		EndTime:        start.Add(30 * time.Minute),
		Frequency:      "daily",
		Interval:       1,
		MaxOccurrences: intPtr(3),
	})
	require.NoError(t, err)

	evRepo := repository.NewEventRepository(testPool)
	events, err := evRepo.List(ctx, user.ID, &def.ID, nil, nil)
	require.NoError(t, err)
	assert.Len(t, events, 3, "should generate exactly max_occurrences instances, not one per day in the 60-day window")
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

func TestRecurringEventRepository_SplitAt_KeepsOccurrenceBudget(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "rec_splitmax")
	def := seedDefaultCalendar(t, testPool, user.ID)
	r := repository.NewRecurringEventRepository(testPool)
	ctx := context.Background()

	start := time.Now().UTC().Truncate(time.Minute).Add(-2 * 24 * time.Hour)
	rec, err := r.Create(ctx, user.ID, def.ID, model.CreateRecurringEventRequest{
		Title:          "Capped Series",
		StartTime:      start,
		EndTime:        start.Add(time.Hour),
		Frequency:      "daily",
		Interval:       1,
		MaxOccurrences: intPtr(5),
	})
	require.NoError(t, err)

	// Fork at occurrence #3, as the handler does (pivot = that instance's start).
	pivot := start.Add(2 * 24 * time.Hour)
	newTitle := "Forked Capped Series"
	newRec, err := r.SplitAt(ctx, rec.ID, user.ID, pivot, model.UpdateRecurrenceRequest{
		Scope: "this_and_following",
		Title: &newTitle,
	})
	require.NoError(t, err)
	require.NotNil(t, newRec.MaxOccurrences, "forked series should not silently lose its occurrence cap")
	assert.Equal(t, 3, *newRec.MaxOccurrences, "fork gets the remaining budget (#3-#5), not a fresh 5")

	evRepo := repository.NewEventRepository(testPool)
	events, err := evRepo.List(ctx, user.ID, &def.ID, nil, nil)
	require.NoError(t, err)
	origCount, newCount := 0, 0
	for _, ev := range events {
		require.NotNil(t, ev.RecurringEventID)
		switch *ev.RecurringEventID {
		case rec.ID:
			origCount++
		case newRec.ID:
			newCount++
		}
	}
	assert.Equal(t, 2, origCount, "original keeps #1-#2")
	assert.Equal(t, 3, newCount, "fork generates #3-#5, so the series still totals 5")
}

func TestRecurringEventRepository_UpdateAll_KeepsDurationAndBound(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "rec_updall")
	def := seedDefaultCalendar(t, testPool, user.ID)
	r := repository.NewRecurringEventRepository(testPool)
	evRepo := repository.NewEventRepository(testPool)
	ctx := context.Background()

	// Offset by 12h so "now" falls between occurrences and the 30-minute shift below
	// can't move an already-started occurrence across it.
	start := time.Now().UTC().Truncate(time.Minute).Add(-14*24*time.Hour - 12*time.Hour)
	rec, err := r.Create(ctx, user.ID, def.ID, model.CreateRecurringEventRequest{
		Title:          "Series",
		StartTime:      start,
		EndTime:        start.Add(time.Hour),
		Frequency:      "daily",
		Interval:       1,
		MaxOccurrences: intPtr(20),
	})
	require.NoError(t, err)

	// Edit future occurrence #16 the way the UI does: send that instance's own times,
	// here moved 30 minutes later and lengthened to 90 minutes.
	instStart := start.Add(15 * 24 * time.Hour)
	newStart := instStart.Add(30 * time.Minute)
	newEnd := newStart.Add(90 * time.Minute)
	title := "Renamed"
	upd, err := r.UpdateAll(ctx, rec.ID, user.ID, instStart, model.UpdateRecurrenceRequest{
		Scope:     "all",
		Title:     &title,
		StartTime: &newStart,
		EndTime:   &newEnd,
	})
	require.NoError(t, err)
	assert.Equal(t, 90*time.Minute, time.Duration(upd.Duration), "duration is the instance's span, not its end minus the series anchor")
	assert.True(t, start.Add(30*time.Minute).Equal(upd.StartTime), "anchor shifts by as much as the instance moved")
	require.NotNil(t, upd.MaxOccurrences, "an all-scope edit must not drop the series' cap")
	assert.Equal(t, 20, *upd.MaxOccurrences)

	events, err := evRepo.List(ctx, user.ID, &def.ID, nil, nil)
	require.NoError(t, err)
	assert.Len(t, events, 20, "kept past instances + regenerated future ones still total the cap")
	for _, ev := range events {
		if ev.StartTime.After(time.Now()) {
			assert.Equal(t, "Renamed", ev.Title)
			assert.Equal(t, 90*time.Minute, ev.EndTime.Sub(ev.StartTime))
		}
	}
}

// linkedInstances returns the events still linked to series recID, in start order.
func linkedInstances(t *testing.T, ctx context.Context, calID, userID, recID uuid.UUID) []model.Event {
	t.Helper()
	events, err := repository.NewEventRepository(testPool).List(ctx, userID, &calID, nil, nil)
	require.NoError(t, err)
	var out []model.Event
	for _, ev := range events {
		if ev.RecurringEventID != nil && *ev.RecurringEventID == recID {
			out = append(out, ev)
		}
	}
	return out
}

func TestRecurringEventRepository_Exceptions_SurviveAllScopeEdit(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "rec_exc")
	def := seedDefaultCalendar(t, testPool, user.ID)
	r := repository.NewRecurringEventRepository(testPool)
	evRepo := repository.NewEventRepository(testPool)
	ctx := context.Background()

	start := time.Now().UTC().Truncate(time.Minute).Add(24 * time.Hour)
	rec, err := r.Create(ctx, user.ID, def.ID, model.CreateRecurringEventRequest{
		Title:          "Series",
		StartTime:      start,
		EndTime:        start.Add(time.Hour),
		Frequency:      "daily",
		Interval:       1,
		MaxOccurrences: intPtr(10),
	})
	require.NoError(t, err)
	inst := linkedInstances(t, ctx, def.ID, user.ID, rec.ID)
	require.Len(t, inst, 10)

	// Delete #3 on its own; move #5 an hour later on its own (as a drag does).
	require.NoError(t, evRepo.Delete(ctx, inst[2].ID, user.ID))
	moved := inst[4].StartTime.Add(time.Hour)
	movedEnd := moved.Add(time.Hour)
	edited, err := evRepo.Update(ctx, inst[4].ID, user.ID, model.UpdateEventRequest{StartTime: &moved, EndTime: &movedEnd})
	require.NoError(t, err)
	assert.Nil(t, edited.RecurringEventID, "an instance edited on its own is detached")

	got, err := r.GetByID(ctx, rec.ID, user.ID)
	require.NoError(t, err)
	require.Len(t, got.Exdates, 2)
	assert.True(t, got.Exdates[0].Equal(inst[2].StartTime))
	assert.True(t, got.Exdates[1].Equal(inst[4].StartTime))

	// Rename the whole series and move it 30 minutes later, editing from #1.
	title := "Renamed"
	newStart := inst[0].StartTime.Add(30 * time.Minute)
	newEnd := newStart.Add(time.Hour)
	_, err = r.UpdateAll(ctx, rec.ID, user.ID, inst[0].StartTime, model.UpdateRecurrenceRequest{
		Scope: "all", Title: &title, StartTime: &newStart, EndTime: &newEnd,
	})
	require.NoError(t, err)

	after := linkedInstances(t, ctx, def.ID, user.ID, rec.ID)
	assert.Len(t, after, 8, "10 occurrences minus the deleted and the detached one — neither regenerated")
	for _, ev := range after {
		assert.False(t, ev.StartTime.Equal(inst[2].StartTime.Add(30*time.Minute)), "deleted occurrence must not reappear")
		assert.False(t, ev.StartTime.Equal(inst[4].StartTime.Add(30*time.Minute)), "detached occurrence must not be duplicated")
		assert.Equal(t, "Renamed", ev.Title)
	}
	standalone, err := evRepo.GetByID(ctx, inst[4].ID, user.ID)
	require.NoError(t, err)
	assert.Equal(t, "Series", standalone.Title, "the detached exception keeps its own fields")
	assert.True(t, standalone.StartTime.Equal(moved))
}

func TestRecurringEventRepository_SplitAt_CarriesExceptions(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "rec_splitexc")
	def := seedDefaultCalendar(t, testPool, user.ID)
	r := repository.NewRecurringEventRepository(testPool)
	evRepo := repository.NewEventRepository(testPool)
	ctx := context.Background()

	start := time.Now().UTC().Truncate(time.Minute).Add(24 * time.Hour)
	rec, err := r.Create(ctx, user.ID, def.ID, model.CreateRecurringEventRequest{
		Title: "Series", StartTime: start, EndTime: start.Add(time.Hour), Frequency: "daily", Interval: 1,
		MaxOccurrences: intPtr(6),
	})
	require.NoError(t, err)
	inst := linkedInstances(t, ctx, def.ID, user.ID, rec.ID)
	require.Len(t, inst, 6)
	require.NoError(t, evRepo.Delete(ctx, inst[3].ID, user.ID)) // #4

	// Fork at #2.
	newTitle := "Forked"
	fork, err := r.SplitAt(ctx, rec.ID, user.ID, inst[1].StartTime, model.UpdateRecurrenceRequest{
		Scope: "this_and_following", Title: &newTitle,
	})
	require.NoError(t, err)
	forked := linkedInstances(t, ctx, def.ID, user.ID, fork.ID)
	assert.Len(t, forked, 4, "#2-#6 minus the deleted #4")
	for _, ev := range forked {
		assert.False(t, ev.StartTime.Equal(inst[3].StartTime), "deleted #4 must not come back in the fork")
	}
	assert.Len(t, linkedInstances(t, ctx, def.ID, user.ID, rec.ID), 1, "original keeps #1")
}

func TestRecurringEventRepository_TruncateAt(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "rec_trunc")
	def := seedDefaultCalendar(t, testPool, user.ID)
	r := repository.NewRecurringEventRepository(testPool)
	ctx := context.Background()

	start := time.Now().UTC().Truncate(time.Minute).Add(-3 * 24 * time.Hour)
	rec, err := r.Create(ctx, user.ID, def.ID, model.CreateRecurringEventRequest{
		Title: "Series", StartTime: start, EndTime: start.Add(time.Hour), Frequency: "daily", Interval: 1,
	})
	require.NoError(t, err)
	inst := linkedInstances(t, ctx, def.ID, user.ID, rec.ID)

	// "This and following" from #3 keeps #1-#2 (past, logged time) and stops the series.
	require.NoError(t, r.TruncateAt(ctx, rec.ID, user.ID, inst[2].StartTime))
	kept := linkedInstances(t, ctx, def.ID, user.ID, rec.ID)
	require.Len(t, kept, 2)
	assert.True(t, kept[1].StartTime.Equal(inst[1].StartTime))
	require.NoError(t, r.GeneratePending(ctx))
	assert.Len(t, linkedInstances(t, ctx, def.ID, user.ID, rec.ID), 2, "the scheduler must not extend a truncated series")

	// From the first occurrence nothing is left, so the rule itself goes.
	require.NoError(t, r.TruncateAt(ctx, rec.ID, user.ID, inst[0].StartTime))
	_, err = r.GetByID(ctx, rec.ID, user.ID)
	assert.ErrorIs(t, err, pgx.ErrNoRows)
}

func TestRecurringEventRepository_TruncateAt_KeepsHistoryAfterAnchorMoved(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "rec_trunc_moved")
	def := seedDefaultCalendar(t, testPool, user.ID)
	r := repository.NewRecurringEventRepository(testPool)
	ctx := context.Background()

	start := time.Now().UTC().Truncate(time.Minute).Add(-5*24*time.Hour - 12*time.Hour)
	rec, err := r.Create(ctx, user.ID, def.ID, model.CreateRecurringEventRequest{
		Title: "Series", StartTime: start, EndTime: start.Add(time.Hour), Frequency: "daily", Interval: 1,
	})
	require.NoError(t, err)
	inst := linkedInstances(t, ctx, def.ID, user.ID, rec.ID)
	past := 0
	for _, ev := range inst {
		if ev.StartTime.Before(time.Now()) {
			past++
		}
	}
	require.Equal(t, 6, past)

	// An all-scope edit moves the series 7 days later: past instances stay, linked, at
	// their old times — all before the new anchor.
	newStart := inst[6].StartTime.Add(7 * 24 * time.Hour)
	newEnd := newStart.Add(time.Hour)
	moved, err := r.UpdateAll(ctx, rec.ID, user.ID, inst[6].StartTime, model.UpdateRecurrenceRequest{
		Scope: "all", StartTime: &newStart, EndTime: &newEnd,
	})
	require.NoError(t, err)

	// "This and following" on the new first occurrence must keep the past ones.
	require.NoError(t, r.TruncateAt(ctx, rec.ID, user.ID, moved.StartTime))
	kept := linkedInstances(t, ctx, def.ID, user.ID, rec.ID)
	assert.Len(t, kept, 6, "earlier occurrences are kept")
	for _, ev := range kept {
		assert.True(t, ev.StartTime.Before(time.Now()))
	}
}

func TestRecurringEventRepository_SeriesDeletesIncludeEditedOccurrences(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "rec_del_exc")
	def := seedDefaultCalendar(t, testPool, user.ID)
	r := repository.NewRecurringEventRepository(testPool)
	evRepo := repository.NewEventRepository(testPool)
	ctx := context.Background()

	start := time.Now().UTC().Truncate(time.Minute).Add(24 * time.Hour)
	rec, err := r.Create(ctx, user.ID, def.ID, model.CreateRecurringEventRequest{
		Title: "Series", StartTime: start, EndTime: start.Add(time.Hour), Frequency: "daily", Interval: 1,
		MaxOccurrences: intPtr(6),
	})
	require.NoError(t, err)
	inst := linkedInstances(t, ctx, def.ID, user.ID, rec.ID)
	require.Len(t, inst, 6)

	// Edit #2 and #5 on their own; move #5 to before #4.
	title := "Edited"
	_, err = evRepo.Update(ctx, inst[1].ID, user.ID, model.UpdateEventRequest{Title: &title})
	require.NoError(t, err)
	early := inst[2].StartTime.Add(time.Hour)
	earlyEnd := early.Add(time.Hour)
	_, err = evRepo.Update(ctx, inst[4].ID, user.ID, model.UpdateEventRequest{StartTime: &early, EndTime: &earlyEnd})
	require.NoError(t, err)

	// "This and following" from #4 removes #5 — matched by the occurrence it was, even
	// though it now starts before #4 — and keeps the edited #2.
	require.NoError(t, r.TruncateAt(ctx, rec.ID, user.ID, inst[3].StartTime))
	_, err = evRepo.GetByID(ctx, inst[4].ID, user.ID)
	assert.ErrorIs(t, err, pgx.ErrNoRows, "edited #5 goes with the following occurrences")
	_, err = evRepo.GetByID(ctx, inst[1].ID, user.ID)
	assert.NoError(t, err, "edited #2 is before the pivot and stays")

	// Deleting the series removes the edited occurrence too.
	require.NoError(t, r.Delete(ctx, rec.ID, user.ID))
	_, err = evRepo.GetByID(ctx, inst[1].ID, user.ID)
	assert.ErrorIs(t, err, pgx.ErrNoRows, "every occurrence, including edited ones, goes with the series")
}

func TestRecurringEventRepository_SplitAt_MovesEditedOccurrences(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "rec_split_exc")
	def := seedDefaultCalendar(t, testPool, user.ID)
	r := repository.NewRecurringEventRepository(testPool)
	evRepo := repository.NewEventRepository(testPool)
	ctx := context.Background()

	start := time.Now().UTC().Truncate(time.Minute).Add(24 * time.Hour)
	rec, err := r.Create(ctx, user.ID, def.ID, model.CreateRecurringEventRequest{
		Title: "Series", StartTime: start, EndTime: start.Add(time.Hour), Frequency: "daily", Interval: 1,
		MaxOccurrences: intPtr(5),
	})
	require.NoError(t, err)
	inst := linkedInstances(t, ctx, def.ID, user.ID, rec.ID)
	title := "Edited"
	_, err = evRepo.Update(ctx, inst[3].ID, user.ID, model.UpdateEventRequest{Title: &title}) // #4
	require.NoError(t, err)

	// Fork at #2, one hour later.
	newStart := inst[1].StartTime.Add(time.Hour)
	fork, err := r.SplitAt(ctx, rec.ID, user.ID, inst[1].StartTime, model.UpdateRecurrenceRequest{
		Scope: "this_and_following", StartTime: &newStart,
	})
	require.NoError(t, err)
	require.Len(t, fork.Exdates, 1)
	assert.True(t, fork.Exdates[0].Equal(inst[3].StartTime.Add(time.Hour)), "the fork excludes its own #3, the edited #4")
	assert.Len(t, linkedInstances(t, ctx, def.ID, user.ID, fork.ID), 3, "#2, #3, #5 regenerated; the edited #4 isn't duplicated")

	// The edited occurrence now belongs to the fork: deleting the fork removes it.
	require.NoError(t, r.Delete(ctx, fork.ID, user.ID))
	_, err = evRepo.GetByID(ctx, inst[3].ID, user.ID)
	assert.ErrorIs(t, err, pgx.ErrNoRows)
}

func TestRecurringEventRepository_RepairLegacyExceptions(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "rec_repair")
	def := seedDefaultCalendar(t, testPool, user.ID)
	r := repository.NewRecurringEventRepository(testPool)
	evRepo := repository.NewEventRepository(testPool)
	ctx := context.Background()

	start := time.Now().UTC().Truncate(time.Minute).Add(24 * time.Hour)
	rec, err := r.Create(ctx, user.ID, def.ID, model.CreateRecurringEventRequest{
		Title: "Series", StartTime: start, EndTime: start.Add(time.Hour), Frequency: "daily", Interval: 1,
		MaxOccurrences: intPtr(6),
	})
	require.NoError(t, err)
	inst := linkedInstances(t, ctx, def.ID, user.ID, rec.ID)
	require.Len(t, inst, 6)

	// Edits as they were made before exceptions were tracked: in place, instance still
	// linked. #2 dragged 3h later, #4 renamed, #5 moved to the evening before.
	legacy := func(id uuid.UUID, set string) {
		_, err := testPool.Exec(ctx, `UPDATE events SET `+set+`, updated_at = updated_at + interval '1 second' WHERE id = $1`, id)
		require.NoError(t, err)
	}
	legacy(inst[1].ID, `start_time = start_time + interval '3 hours', end_time = end_time + interval '3 hours'`)
	legacy(inst[3].ID, `title = 'Renamed'`)
	legacy(inst[4].ID, `start_time = start_time - interval '20 hours', end_time = end_time - interval '20 hours'`)

	n, err := r.RepairLegacyExceptions(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, n)
	n, err = r.RepairLegacyExceptions(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, n, "a repaired database has nothing left to repair")

	got, err := r.GetByID(ctx, rec.ID, user.ID)
	require.NoError(t, err)
	var excluded []time.Time
	for _, e := range got.Exdates {
		excluded = append(excluded, e.UTC())
	}
	assert.ElementsMatch(t, []time.Time{inst[1].StartTime.UTC(), inst[3].StartTime.UTC(), inst[4].StartTime.UTC()}, excluded,
		"each edited instance's own occurrence is excluded")
	for _, i := range []int{1, 3, 4} {
		var detachedFrom *uuid.UUID
		var originalStart *time.Time
		require.NoError(t, testPool.QueryRow(ctx,
			`SELECT detached_from, original_start FROM events WHERE id = $1`, inst[i].ID).Scan(&detachedFrom, &originalStart))
		require.NotNil(t, detachedFrom)
		assert.Equal(t, rec.ID, *detachedFrom)
		require.NotNil(t, originalStart)
		assert.True(t, originalStart.Equal(inst[i].StartTime), "#%d's original occurrence", i+1)
	}

	// A series-wide rename now keeps the edits and doesn't bring the occurrences back.
	title := "All renamed"
	s1, e1 := inst[0].StartTime, inst[0].EndTime
	_, err = r.UpdateAll(ctx, rec.ID, user.ID, inst[0].StartTime, model.UpdateRecurrenceRequest{
		Scope: "all", Title: &title, StartTime: &s1, EndTime: &e1,
	})
	require.NoError(t, err)
	linked := linkedInstances(t, ctx, def.ID, user.ID, rec.ID)
	assert.Len(t, linked, 3, "#1, #3, #6; the three edited occurrences aren't regenerated")
	moved, err := evRepo.GetByID(ctx, inst[1].ID, user.ID)
	require.NoError(t, err)
	assert.True(t, moved.StartTime.Equal(inst[1].StartTime.Add(3*time.Hour)), "the dragged instance keeps its time")
	assert.Equal(t, "Series", moved.Title)

	// And deleting the series takes them with it.
	require.NoError(t, r.Delete(ctx, rec.ID, user.ID))
	for _, i := range []int{1, 3, 4} {
		_, err := evRepo.GetByID(ctx, inst[i].ID, user.ID)
		assert.ErrorIs(t, err, pgx.ErrNoRows)
	}
}

func TestRecurringEventRepository_ExtendThrough(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "rec_extend")
	other := seedUser(t, testPool, "rec_extend_other")
	def := seedDefaultCalendar(t, testPool, user.ID)
	otherCal := seedDefaultCalendar(t, testPool, other.ID)
	r := repository.NewRecurringEventRepository(testPool)
	ctx := context.Background()

	start := time.Now().UTC().Truncate(time.Minute).Add(time.Hour)
	weekly := model.CreateRecurringEventRequest{Title: "Weekly", StartTime: start, EndTime: start.Add(time.Hour), Frequency: "weekly", Interval: 1}
	rec, err := r.Create(ctx, user.ID, def.ID, weekly)
	require.NoError(t, err)
	capped := weekly
	capped.Title, capped.MaxOccurrences = "Capped", intPtr(12)
	cappedRec, err := r.Create(ctx, user.ID, def.ID, capped)
	require.NoError(t, err)
	theirs, err := r.Create(ctx, other.ID, otherCal.ID, weekly)
	require.NoError(t, err)
	require.Len(t, linkedInstances(t, ctx, def.ID, user.ID, rec.ID), 9, "60-day window of weekly occurrences")

	// A view ~6 months ahead.
	until := start.Add(26 * 7 * 24 * time.Hour)
	require.NoError(t, r.ExtendThrough(ctx, user.ID, nil, until))
	assert.Len(t, linkedInstances(t, ctx, def.ID, user.ID, rec.ID), 27, "occurrences materialized through the viewed range")
	assert.Len(t, linkedInstances(t, ctx, def.ID, user.ID, cappedRec.ID), 12, "still bounded by max_occurrences")
	assert.Len(t, linkedInstances(t, ctx, otherCal.ID, other.ID, theirs.ID), 9, "another user's series isn't touched")

	// The scheduler doesn't move generated_until back or duplicate anything.
	require.NoError(t, r.GeneratePending(ctx))
	assert.Len(t, linkedInstances(t, ctx, def.ID, user.ID, rec.ID), 27)

	// A series-wide edit regenerates only the rolling window; viewing ahead again
	// restores the rest, renamed.
	title := "Renamed"
	inst := linkedInstances(t, ctx, def.ID, user.ID, rec.ID)
	_, err = r.UpdateAll(ctx, rec.ID, user.ID, inst[0].StartTime, model.UpdateRecurrenceRequest{Scope: "all", Title: &title})
	require.NoError(t, err)
	require.NoError(t, r.ExtendThrough(ctx, user.ID, &def.ID, until))
	after := linkedInstances(t, ctx, def.ID, user.ID, rec.ID)
	assert.Len(t, after, 27)
	for _, ev := range after {
		assert.Equal(t, "Renamed", ev.Title)
	}

	// Far-future ranges are capped rather than generating decades of rows.
	require.NoError(t, r.ExtendThrough(ctx, user.ID, &def.ID, start.AddDate(50, 0, 0)))
	n := len(linkedInstances(t, ctx, def.ID, user.ID, rec.ID))
	assert.InDelta(t, 3*52, n, 3, "about three years of weekly occurrences")
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
