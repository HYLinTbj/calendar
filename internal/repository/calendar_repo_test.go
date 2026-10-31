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

func TestCalendarRepository_Create(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "a")
	r := repository.NewCalendarRepository(testPool)
	ctx := context.Background()

	cal, err := r.Create(ctx, user.ID, model.CreateCalendarRequest{Name: "Work", Description: "Work calendar", Color: "#059669"})
	require.NoError(t, err)
	assert.Equal(t, "Work", cal.Name)
	assert.Equal(t, "#059669", cal.Color)
	assert.Equal(t, user.ID, cal.OwnerID)
	assert.False(t, cal.IsDefault)
}

func TestCalendarRepository_Create_BlankColorGetsDefault(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "a2")
	r := repository.NewCalendarRepository(testPool)
	ctx := context.Background()

	for _, color := range []string{"", "  "} {
		cal, err := r.Create(ctx, user.ID, model.CreateCalendarRequest{Name: "Work" + color, Color: color})
		require.NoError(t, err)
		assert.Equal(t, model.DefaultCalendarColor, cal.Color)
	}
}

func TestCalendarRepository_CreateDefault(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "b")
	r := repository.NewCalendarRepository(testPool)
	ctx := context.Background()

	cal, err := r.CreateDefault(ctx, user.ID)
	require.NoError(t, err)
	assert.True(t, cal.IsDefault)
	assert.Equal(t, "My Calendar", cal.Name)
	assert.Equal(t, model.DefaultCalendarColor, cal.Color)
}

func TestCalendarRepository_GetByID(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "c")
	cal := seedCalendar(t, testPool, user.ID, "Personal")
	r := repository.NewCalendarRepository(testPool)
	ctx := context.Background()

	got, err := r.GetByID(ctx, cal.ID, user.ID)
	require.NoError(t, err)
	assert.Equal(t, "Personal", got.Name)
}

func TestCalendarRepository_GetDefault(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "d")
	def := seedDefaultCalendar(t, testPool, user.ID)
	r := repository.NewCalendarRepository(testPool)
	ctx := context.Background()

	got, err := r.GetDefault(ctx, user.ID)
	require.NoError(t, err)
	assert.Equal(t, def.ID, got.ID)
	assert.True(t, got.IsDefault)
}

func TestCalendarRepository_Update_SwitchDefault(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "e")
	old := seedDefaultCalendar(t, testPool, user.ID)
	newCal := seedCalendar(t, testPool, user.ID, "New Default")
	r := repository.NewCalendarRepository(testPool)
	ctx := context.Background()

	isDefault, color := true, "#dc2626"
	updated, err := r.Update(ctx, newCal.ID, user.ID, model.UpdateCalendarRequest{IsDefault: &isDefault, Color: &color})
	require.NoError(t, err)
	assert.True(t, updated.IsDefault)
	assert.Equal(t, color, updated.Color)
	assert.Equal(t, "New Default", updated.Name)

	// Old default should no longer be default
	oldNow, err := r.GetByID(ctx, old.ID, user.ID)
	require.NoError(t, err)
	assert.False(t, oldNow.IsDefault)
}

func TestCalendarRepository_Update_RenameAndRecolor(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "e2")
	cal := seedCalendar(t, testPool, user.ID, "Old")
	r := repository.NewCalendarRepository(testPool)
	ctx := context.Background()

	name, color := "Renamed", "#7c3aed"
	updated, err := r.Update(ctx, cal.ID, user.ID, model.UpdateCalendarRequest{Name: &name, Color: &color})
	require.NoError(t, err)
	assert.Equal(t, name, updated.Name)
	assert.Equal(t, color, updated.Color)

	got, err := r.GetByID(ctx, cal.ID, user.ID)
	require.NoError(t, err)
	assert.Equal(t, color, got.Color)

	// Fields left out keep their values.
	updated, err = r.Update(ctx, cal.ID, user.ID, model.UpdateCalendarRequest{})
	require.NoError(t, err)
	assert.Equal(t, name, updated.Name)
	assert.Equal(t, color, updated.Color)
}

func TestCalendarRepository_GetByIDOrShared_ReadsColor(t *testing.T) {
	truncateAll(t, testPool)
	owner := seedUser(t, testPool, "s1")
	guest := seedUser(t, testPool, "s2")
	r := repository.NewCalendarRepository(testPool)
	ctx := context.Background()

	cal, err := r.Create(ctx, owner.ID, model.CreateCalendarRequest{Name: "Shared", Color: "#0891b2"})
	require.NoError(t, err)
	_, err = repository.NewCalendarShareRepository(testPool).Create(ctx, cal.ID, owner.ID, guest.ID, "view")
	require.NoError(t, err)

	got, perm, err := r.GetByIDOrShared(ctx, cal.ID, guest.ID)
	require.NoError(t, err)
	assert.Equal(t, "view", perm)
	assert.Equal(t, "#0891b2", got.Color)
	assert.Equal(t, "Shared", got.Name)
}

func TestCalendarRepository_Delete_MovesEventsToDefault(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "f")
	def := seedDefaultCalendar(t, testPool, user.ID)
	extra := seedCalendar(t, testPool, user.ID, "Extra")

	// Create an event in the extra calendar
	ev := seedEvent(t, testPool, user.ID, extra.ID, "My Event")

	r := repository.NewCalendarRepository(testPool)
	ctx := context.Background()

	err := r.Delete(ctx, extra.ID, user.ID)
	require.NoError(t, err)

	// Event should now be in the default calendar
	eventRepo := repository.NewEventRepository(testPool)
	got, err := eventRepo.GetByID(ctx, ev.ID, user.ID)
	require.NoError(t, err)
	assert.Equal(t, def.ID, got.CalendarID)
}

// lockCalendarsOf begins a transaction that holds the lock Delete and a default
// switch take first, so a test can stage the change of a concurrent one.
func lockCalendarsOf(t *testing.T, userID uuid.UUID) pgx.Tx {
	t.Helper()
	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { tx.Rollback(ctx) })
	_, err = tx.Exec(ctx, `SELECT 1 FROM users WHERE id = $1 FOR NO KEY UPDATE`, userID)
	require.NoError(t, err)
	return tx
}

// switchDefault makes calID the user's default in tx, as Update does.
func switchDefault(t *testing.T, tx pgx.Tx, userID, calID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	_, err := tx.Exec(ctx, `UPDATE calendars SET is_default = false WHERE owner_id = $1 AND is_default`, userID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `UPDATE calendars SET is_default = true WHERE id = $1`, calID)
	require.NoError(t, err)
}

// commitWhenWaiting runs op, waits until op is blocked by tx, commits tx and returns
// op's error. It fails at once if op returns without waiting.
func commitWhenWaiting(t *testing.T, tx pgx.Tx, op func() error) error {
	t.Helper()
	ctx := context.Background()
	var pid int
	require.NoError(t, tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid))
	done := make(chan error, 1)
	go func() { done <- op() }()
	deadline := time.After(10 * time.Second)
	for {
		var waiting bool
		require.NoError(t, testPool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE $1 = ANY (pg_blocking_pids(pid)))`, pid,
		).Scan(&waiting))
		if waiting {
			break
		}
		select {
		case err := <-done:
			require.FailNow(t, "returned without waiting for the concurrent change", "error: %v", err)
		case <-deadline:
			require.FailNow(t, "never waited for the concurrent change")
		case <-time.After(20 * time.Millisecond):
		}
	}
	require.NoError(t, tx.Commit(ctx))
	return <-done
}

// A calendar made the default while Delete runs (say, from another tab) is kept.
func TestCalendarRepository_Delete_KeepsCalendarMadeDefaultMeanwhile(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "f2")
	def := seedDefaultCalendar(t, testPool, user.ID)
	cal := seedCalendar(t, testPool, user.ID, "Soon default")
	ev := seedEvent(t, testPool, user.ID, cal.ID, "Stays put")
	r := repository.NewCalendarRepository(testPool)
	ctx := context.Background()

	tx := lockCalendarsOf(t, user.ID)
	switchDefault(t, tx, user.ID, cal.ID)

	err := commitWhenWaiting(t, tx, func() error { return r.Delete(ctx, cal.ID, user.ID) })
	assert.ErrorIs(t, err, repository.ErrDeleteDefault)
	got, err := r.GetByID(ctx, cal.ID, user.ID)
	require.NoError(t, err)
	assert.True(t, got.IsDefault)
	moved, err := repository.NewEventRepository(testPool).GetByID(ctx, ev.ID, user.ID)
	require.NoError(t, err)
	assert.Equal(t, cal.ID, moved.CalendarID, "nothing moved to the old default %s", def.ID)
}

// A calendar another request deletes meanwhile is not found, as it would be after.
func TestCalendarRepository_Delete_DeletedMeanwhile(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "f3")
	def := seedDefaultCalendar(t, testPool, user.ID)
	cal := seedCalendar(t, testPool, user.ID, "Twice")
	seedEvent(t, testPool, user.ID, cal.ID, "Moved once")
	r := repository.NewCalendarRepository(testPool)
	ctx := context.Background()

	tx := lockCalendarsOf(t, user.ID)
	_, err := tx.Exec(ctx, `UPDATE events SET calendar_id = $1 WHERE calendar_id = $2`, def.ID, cal.ID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `DELETE FROM calendars WHERE id = $1`, cal.ID)
	require.NoError(t, err)

	err = commitWhenWaiting(t, tx, func() error { return r.Delete(ctx, cal.ID, user.ID) })
	assert.ErrorIs(t, err, pgx.ErrNoRows)
}

// Delete moves events to the default as of its own transaction, not an older one.
func TestCalendarRepository_Delete_UsesDefaultSwitchedMeanwhile(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "f4")
	seedDefaultCalendar(t, testPool, user.ID)
	next := seedCalendar(t, testPool, user.ID, "Next default")
	cal := seedCalendar(t, testPool, user.ID, "Going")
	ev := seedEvent(t, testPool, user.ID, cal.ID, "Moves")
	r := repository.NewCalendarRepository(testPool)
	ctx := context.Background()

	tx := lockCalendarsOf(t, user.ID)
	switchDefault(t, tx, user.ID, next.ID)

	require.NoError(t, commitWhenWaiting(t, tx, func() error { return r.Delete(ctx, cal.ID, user.ID) }))
	moved, err := repository.NewEventRepository(testPool).GetByID(ctx, ev.ID, user.ID)
	require.NoError(t, err)
	assert.Equal(t, next.ID, moved.CalendarID)
}

// Two default switches at once (say, from two tabs) end with the later one's choice.
func TestCalendarRepository_Update_SwitchDefaultMeanwhile(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "f5")
	seedDefaultCalendar(t, testPool, user.ID)
	first := seedCalendar(t, testPool, user.ID, "First")
	second := seedCalendar(t, testPool, user.ID, "Second")
	r := repository.NewCalendarRepository(testPool)
	ctx := context.Background()

	tx := lockCalendarsOf(t, user.ID)
	switchDefault(t, tx, user.ID, first.ID)

	isDefault := true
	err := commitWhenWaiting(t, tx, func() error {
		_, err := r.Update(ctx, second.ID, user.ID, model.UpdateCalendarRequest{IsDefault: &isDefault})
		return err
	})
	require.NoError(t, err)
	cals, err := r.List(ctx, user.ID)
	require.NoError(t, err)
	assert.Equal(t, second.ID, cals[0].ID)
	assert.True(t, cals[0].IsDefault)
	assert.False(t, cals[1].IsDefault || cals[2].IsDefault)
}

// Concurrent updates of different fields (a rename and a recolor) both stick.
func TestCalendarRepository_Update_KeepsConcurrentEdit(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "f6")
	cal := seedCalendar(t, testPool, user.ID, "Old")
	r := repository.NewCalendarRepository(testPool)
	ctx := context.Background()

	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { tx.Rollback(ctx) })
	_, err = tx.Exec(ctx, `UPDATE calendars SET name = 'Renamed' WHERE id = $1`, cal.ID)
	require.NoError(t, err)

	color := "#dc2626"
	err = commitWhenWaiting(t, tx, func() error {
		_, err := r.Update(ctx, cal.ID, user.ID, model.UpdateCalendarRequest{Color: &color})
		return err
	})
	require.NoError(t, err)
	got, err := r.GetByID(ctx, cal.ID, user.ID)
	require.NoError(t, err)
	assert.Equal(t, "Renamed", got.Name)
	assert.Equal(t, color, got.Color)
}

func TestCalendarRepository_Delete_DefaultReturnsError(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "g")
	def := seedDefaultCalendar(t, testPool, user.ID)
	r := repository.NewCalendarRepository(testPool)
	ctx := context.Background()

	err := r.Delete(ctx, def.ID, user.ID)
	assert.ErrorIs(t, err, repository.ErrDeleteDefault)
}
