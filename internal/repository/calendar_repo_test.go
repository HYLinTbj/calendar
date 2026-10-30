//go:build integration

package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/hylin/calendar/internal/model"
	"github.com/hylin/calendar/internal/repository"
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

// A calendar made the default while Delete runs (say, from another tab) is kept.
// Delete checks is_default before its transaction, so its DELETE checks again.
func TestCalendarRepository_Delete_KeepsCalendarMadeDefaultMeanwhile(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "f2")
	def := seedDefaultCalendar(t, testPool, user.ID)
	cal := seedCalendar(t, testPool, user.ID, "Soon default")
	ev := seedEvent(t, testPool, user.ID, cal.ID, "Stays put")
	r := repository.NewCalendarRepository(testPool)
	ctx := context.Background()

	// Switch the default as Update does, but hold the commit until Delete waits on it.
	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `UPDATE calendars SET is_default = false WHERE id = $1`, def.ID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `UPDATE calendars SET is_default = true WHERE id = $1`, cal.ID)
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() { done <- r.Delete(ctx, cal.ID, user.ID) }()
	require.Eventually(t, func() bool {
		var waiting int
		_ = testPool.QueryRow(ctx, `SELECT count(*) FROM pg_locks WHERE NOT granted`).Scan(&waiting)
		return waiting > 0
	}, 10*time.Second, 20*time.Millisecond, "Delete should block on the calendar row")
	require.NoError(t, tx.Commit(ctx))

	assert.ErrorIs(t, <-done, repository.ErrDeleteDefault)
	got, err := r.GetByID(ctx, cal.ID, user.ID)
	require.NoError(t, err)
	assert.True(t, got.IsDefault)
	moved, err := repository.NewEventRepository(testPool).GetByID(ctx, ev.ID, user.ID)
	require.NoError(t, err)
	assert.Equal(t, cal.ID, moved.CalendarID, "the move to the old default rolled back")
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
