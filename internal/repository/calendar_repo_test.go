//go:build integration

package repository_test

import (
	"context"
	"testing"

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

	cal, err := r.Create(ctx, user.ID, "Work", "Work calendar")
	require.NoError(t, err)
	assert.Equal(t, "Work", cal.Name)
	assert.Equal(t, user.ID, cal.OwnerID)
	assert.False(t, cal.IsDefault)
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

	isDefault := true
	updated, err := r.Update(ctx, newCal.ID, user.ID, model.UpdateCalendarRequest{IsDefault: &isDefault})
	require.NoError(t, err)
	assert.True(t, updated.IsDefault)

	// Old default should no longer be default
	oldNow, err := r.GetByID(ctx, old.ID, user.ID)
	require.NoError(t, err)
	assert.False(t, oldNow.IsDefault)
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

func TestCalendarRepository_Delete_DefaultReturnsError(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "g")
	def := seedDefaultCalendar(t, testPool, user.ID)
	r := repository.NewCalendarRepository(testPool)
	ctx := context.Background()

	err := r.Delete(ctx, def.ID, user.ID)
	assert.ErrorIs(t, err, repository.ErrDeleteDefault)
}
