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

func TestEventRepository_Create(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "ev_a")
	def := seedDefaultCalendar(t, testPool, user.ID)
	r := repository.NewEventRepository(testPool)
	ctx := context.Background()

	ev, err := r.Create(ctx, user.ID, def.ID, model.CreateEventRequest{
		Title:     "Standup",
		StartTime: time.Date(2024, 6, 15, 9, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2024, 6, 15, 9, 30, 0, 0, time.UTC),
		Reminders: []model.Reminder{{Minutes: 10, Method: "email"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "Standup", ev.Title)
	assert.Equal(t, def.ID, ev.CalendarID)
	require.Len(t, ev.Reminders, 1)
	assert.Equal(t, 10, ev.Reminders[0].Minutes)
}

func TestEventRepository_GetByID_AccessControl(t *testing.T) {
	truncateAll(t, testPool)
	owner := seedUser(t, testPool, "ev_b_owner")
	other := seedUser(t, testPool, "ev_b_other")
	def := seedDefaultCalendar(t, testPool, owner.ID)
	ev := seedEvent(t, testPool, owner.ID, def.ID, "Private Event")
	r := repository.NewEventRepository(testPool)
	ctx := context.Background()

	// Owner can see their own event
	got, err := r.GetByID(ctx, ev.ID, owner.ID)
	require.NoError(t, err)
	assert.Equal(t, ev.ID, got.ID)

	// Another user without share cannot see the event
	_, err = r.GetByID(ctx, ev.ID, other.ID)
	assert.Error(t, err, "unshared user should not see the event")
}

func TestEventRepository_PrivacyMasking(t *testing.T) {
	truncateAll(t, testPool)
	owner := seedUser(t, testPool, "ev_c_owner")
	viewer := seedUser(t, testPool, "ev_c_viewer")
	def := seedDefaultCalendar(t, testPool, owner.ID)

	// Share the calendar with viewer
	shareRepo := repository.NewCalendarShareRepository(testPool)
	_, err := shareRepo.Create(context.Background(), def.ID, owner.ID, viewer.ID, "view")
	require.NoError(t, err)

	// Create a private event
	evRepo := repository.NewEventRepository(testPool)
	ctx := context.Background()
	private := "private"
	ev, err := evRepo.Create(ctx, owner.ID, def.ID, model.CreateEventRequest{
		Title:      "Secret Meeting",
		StartTime:  time.Date(2024, 6, 15, 9, 0, 0, 0, time.UTC),
		EndTime:    time.Date(2024, 6, 15, 10, 0, 0, 0, time.UTC),
		Visibility: private,
	})
	require.NoError(t, err)

	// Owner sees full details
	got, err := evRepo.GetByID(ctx, ev.ID, owner.ID)
	require.NoError(t, err)
	assert.Equal(t, "Secret Meeting", got.Title)

	// Viewer gets masked event
	masked, err := evRepo.GetByID(ctx, ev.ID, viewer.ID)
	require.NoError(t, err)
	assert.Equal(t, "Busy", masked.Title)
	assert.Empty(t, masked.Description)
	assert.Empty(t, masked.Attendees)
}

func TestEventRepository_List_FilterByCalendar(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "ev_d")
	cal1 := seedCalendar(t, testPool, user.ID, "Cal1")
	cal2 := seedCalendar(t, testPool, user.ID, "Cal2")
	seedEvent(t, testPool, user.ID, cal1.ID, "Event in Cal1")
	seedEvent(t, testPool, user.ID, cal2.ID, "Event in Cal2")
	r := repository.NewEventRepository(testPool)
	ctx := context.Background()

	events, err := r.List(ctx, user.ID, &cal1.ID, nil, nil)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "Event in Cal1", events[0].Title)
}

func TestEventRepository_List_FilterByTimeRange(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "ev_e")
	cal := seedCalendar(t, testPool, user.ID, "Cal")
	r := repository.NewEventRepository(testPool)
	ctx := context.Background()

	// Event in June
	_, err := r.Create(ctx, user.ID, cal.ID, model.CreateEventRequest{
		Title:     "June Event",
		StartTime: time.Date(2024, 6, 15, 9, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2024, 6, 15, 10, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)

	// Event in July
	_, err = r.Create(ctx, user.ID, cal.ID, model.CreateEventRequest{
		Title:     "July Event",
		StartTime: time.Date(2024, 7, 15, 9, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2024, 7, 15, 10, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)

	from := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 6, 30, 23, 59, 59, 0, time.UTC)
	events, err := r.List(ctx, user.ID, nil, &from, &to)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "June Event", events[0].Title)
}

func TestEventRepository_Search_FullText(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "ev_f")
	cal := seedCalendar(t, testPool, user.ID, "Cal")
	r := repository.NewEventRepository(testPool)
	ctx := context.Background()

	_, err := r.Create(ctx, user.ID, cal.ID, model.CreateEventRequest{
		Title:     "Quarterly Review Meeting",
		StartTime: time.Date(2024, 6, 15, 9, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2024, 6, 15, 10, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)

	results, err := r.Search(ctx, user.ID, "quarterly", nil)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "Quarterly Review Meeting", results[0].Title)
}

func TestEventRepository_Search_PrivateEventHidden(t *testing.T) {
	truncateAll(t, testPool)
	owner := seedUser(t, testPool, "ev_g_owner")
	viewer := seedUser(t, testPool, "ev_g_viewer")
	def := seedDefaultCalendar(t, testPool, owner.ID)

	shareRepo := repository.NewCalendarShareRepository(testPool)
	_, err := shareRepo.Create(context.Background(), def.ID, owner.ID, viewer.ID, "view")
	require.NoError(t, err)

	evRepo := repository.NewEventRepository(testPool)
	ctx := context.Background()
	private := "private"
	_, err = evRepo.Create(ctx, owner.ID, def.ID, model.CreateEventRequest{
		Title:      "Secret Quarterly Meeting",
		StartTime:  time.Date(2024, 6, 15, 9, 0, 0, 0, time.UTC),
		EndTime:    time.Date(2024, 6, 15, 10, 0, 0, 0, time.UTC),
		Visibility: private,
	})
	require.NoError(t, err)

	// Owner finds their own private event
	ownerResults, err := evRepo.Search(ctx, owner.ID, "secret quarterly", nil)
	require.NoError(t, err)
	assert.Len(t, ownerResults, 1)

	// Viewer cannot find the private event
	viewerResults, err := evRepo.Search(ctx, viewer.ID, "secret quarterly", nil)
	require.NoError(t, err)
	assert.Empty(t, viewerResults)
}

func TestEventRepository_GetBusySlots_NoShare(t *testing.T) {
	truncateAll(t, testPool)
	owner := seedUser(t, testPool, "ev_h_owner")
	requester := seedUser(t, testPool, "ev_h_req")
	def := seedDefaultCalendar(t, testPool, owner.ID)
	r := repository.NewEventRepository(testPool)
	ctx := context.Background()

	_, err := r.Create(ctx, owner.ID, def.ID, model.CreateEventRequest{
		Title:     "Busy",
		StartTime: time.Date(2024, 6, 15, 9, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2024, 6, 15, 10, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)

	from := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 6, 30, 0, 0, 0, 0, time.UTC)
	_, hasAccess, err := r.GetBusySlots(ctx, requester.ID, owner.ID, from, to)
	require.NoError(t, err)
	assert.False(t, hasAccess, "requester has no calendar share, should not have access")
}

func TestEventRepository_GetBusySlots_WithShare(t *testing.T) {
	truncateAll(t, testPool)
	owner := seedUser(t, testPool, "ev_i_owner")
	requester := seedUser(t, testPool, "ev_i_req")
	def := seedDefaultCalendar(t, testPool, owner.ID)

	shareRepo := repository.NewCalendarShareRepository(testPool)
	_, err := shareRepo.Create(context.Background(), def.ID, owner.ID, requester.ID, "view")
	require.NoError(t, err)

	r := repository.NewEventRepository(testPool)
	ctx := context.Background()

	_, err = r.Create(ctx, owner.ID, def.ID, model.CreateEventRequest{
		Title:     "Busy Block",
		StartTime: time.Date(2024, 6, 15, 9, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2024, 6, 15, 10, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)

	from := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 6, 30, 0, 0, 0, 0, time.UTC)
	slots, hasAccess, err := r.GetBusySlots(ctx, requester.ID, owner.ID, from, to)
	require.NoError(t, err)
	assert.True(t, hasAccess)
	assert.Len(t, slots, 1)
}

func TestEventRepository_Update(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "ev_j")
	cal := seedCalendar(t, testPool, user.ID, "Cal")
	ev := seedEvent(t, testPool, user.ID, cal.ID, "Original Title")
	r := repository.NewEventRepository(testPool)
	ctx := context.Background()

	newTitle := "Updated Title"
	updated, err := r.Update(ctx, ev.ID, user.ID, model.UpdateEventRequest{Title: &newTitle})
	require.NoError(t, err)
	assert.Equal(t, "Updated Title", updated.Title)
}

func TestEventRepository_Delete(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "ev_k")
	cal := seedCalendar(t, testPool, user.ID, "Cal")
	ev := seedEvent(t, testPool, user.ID, cal.ID, "To Delete")
	r := repository.NewEventRepository(testPool)
	ctx := context.Background()

	err := r.Delete(ctx, ev.ID, user.ID)
	require.NoError(t, err)

	_, err = r.GetByID(ctx, ev.ID, user.ID)
	assert.Error(t, err)
}
