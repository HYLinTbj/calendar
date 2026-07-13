//go:build integration

package handler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/hylin/calendar/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEventHandler_Create_DefaultCalendar(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "evh_a")

	w := Do(t, testRouter, "POST", "/events", token, map[string]interface{}{
		"title":      "Standup",
		"start_time": "2024-06-15T09:00:00Z",
		"end_time":   "2024-06-15T09:30:00Z",
	})
	assert.Equal(t, http.StatusCreated, w.Code)

	var resp struct {
		ID         uuid.UUID `json:"id"`
		Title      string    `json:"title"`
		CalendarID uuid.UUID `json:"calendar_id"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "Standup", resp.Title)
	assert.NotEmpty(t, resp.CalendarID)
}

func TestEventHandler_Create_ExplicitCalendar_Owned(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "evh_b")

	// Create a second calendar
	wCal := Do(t, testRouter, "POST", "/calendars", token, map[string]string{"name": "Work"})
	require.Equal(t, http.StatusCreated, wCal.Code)
	var calResp struct {
		ID uuid.UUID `json:"id"`
	}
	require.NoError(t, json.NewDecoder(wCal.Body).Decode(&calResp))

	w := Do(t, testRouter, "POST", "/events", token, map[string]interface{}{
		"calendar_id": calResp.ID,
		"title":       "Work Event",
		"start_time":  "2024-06-15T09:00:00Z",
		"end_time":    "2024-06-15T10:00:00Z",
	})
	assert.Equal(t, http.StatusCreated, w.Code)

	var resp struct {
		CalendarID uuid.UUID `json:"calendar_id"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, calResp.ID, resp.CalendarID)
}

func TestEventHandler_Create_EditShare_Allowed(t *testing.T) {
	truncateAll(t, testPool)
	ownerID, ownerToken := MustRegisterAndLogin(t, testRouter, "evh_c_owner")
	_, guestToken := MustRegisterAndLogin(t, testRouter, "evh_c_guest")

	// Get owner's default calendar
	calRepo := repository.NewCalendarRepository(testPool)
	def, err := calRepo.GetDefault(context.Background(), ownerID)
	require.NoError(t, err)

	// Owner shares their default calendar with guest as 'edit'
	Do(t, testRouter, "POST", fmt.Sprintf("/calendars/%s/shares", def.ID), ownerToken, map[string]string{
		"email":      "user_evh_c_guest@example.com",
		"permission": "edit",
	})

	// Guest creates an event in owner's calendar
	wEv := Do(t, testRouter, "POST", "/events", guestToken, map[string]interface{}{
		"calendar_id": def.ID,
		"title":       "Guest Event",
		"start_time":  "2024-06-15T09:00:00Z",
		"end_time":    "2024-06-15T10:00:00Z",
	})
	assert.Equal(t, http.StatusCreated, wEv.Code)
}

func TestEventHandler_Create_ViewOnlyShare_Denied(t *testing.T) {
	truncateAll(t, testPool)
	ownerID, ownerToken := MustRegisterAndLogin(t, testRouter, "evh_d_owner")
	_, guestToken := MustRegisterAndLogin(t, testRouter, "evh_d_guest")

	calRepo := repository.NewCalendarRepository(testPool)
	def, err := calRepo.GetDefault(context.Background(), ownerID)
	require.NoError(t, err)

	// Owner shares with view-only permission
	Do(t, testRouter, "POST", fmt.Sprintf("/calendars/%s/shares", def.ID), ownerToken, map[string]string{
		"email":      "user_evh_d_guest@example.com",
		"permission": "view",
	})

	// Guest tries to create an event — should be denied
	wEv := Do(t, testRouter, "POST", "/events", guestToken, map[string]interface{}{
		"calendar_id": def.ID,
		"title":       "Unauthorized",
		"start_time":  "2024-06-15T09:00:00Z",
		"end_time":    "2024-06-15T10:00:00Z",
	})
	assert.Equal(t, http.StatusBadRequest, wEv.Code)
}

func TestEventHandler_Create_WithReminders_ScheduledInRedis(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "evh_e")

	w := Do(t, testRouter, "POST", "/events", token, map[string]interface{}{
		"title":      "Reminder Test",
		"start_time": "2024-06-15T09:00:00Z",
		"end_time":   "2024-06-15T10:00:00Z",
		"reminders":  []map[string]interface{}{{"minutes": 15, "method": "email"}},
	})
	assert.Equal(t, http.StatusCreated, w.Code)

	// Reminder should be enqueued in Redis
	ctx := context.Background()
	count, err := testRDB.ZCard(ctx, "reminders").Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}

func TestEventHandler_Create_WithAttendees_InvitationsCreated(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "evh_f")

	w := Do(t, testRouter, "POST", "/events", token, map[string]interface{}{
		"title":      "Team Meeting",
		"start_time": "2024-06-15T09:00:00Z",
		"end_time":   "2024-06-15T10:00:00Z",
		"attendees":  []string{"alice@example.com", "bob@example.com"},
	})
	require.Equal(t, http.StatusCreated, w.Code)

	var evResp struct {
		ID uuid.UUID `json:"id"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&evResp))

	// Check invitation rows exist
	invRepo := repository.NewInvitationRepository(testPool)
	statuses, err := invRepo.ListStatusesByEvent(context.Background(), evResp.ID)
	require.NoError(t, err)
	assert.Len(t, statuses, 2)
}

func TestEventHandler_GetByID_PrivateEvent_Owner(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "evh_g")

	w := Do(t, testRouter, "POST", "/events", token, map[string]interface{}{
		"title":      "Secret Meeting",
		"start_time": "2024-06-15T09:00:00Z",
		"end_time":   "2024-06-15T10:00:00Z",
		"visibility": "private",
	})
	require.Equal(t, http.StatusCreated, w.Code)
	var evResp struct {
		ID uuid.UUID `json:"id"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&evResp))

	// Owner sees full details
	wGet := Do(t, testRouter, "GET", fmt.Sprintf("/events/%s", evResp.ID), token, nil)
	assert.Equal(t, http.StatusOK, wGet.Code)

	var got struct {
		Title string `json:"title"`
	}
	require.NoError(t, json.NewDecoder(wGet.Body).Decode(&got))
	assert.Equal(t, "Secret Meeting", got.Title)
}

func TestEventHandler_GetByID_PrivateEvent_Viewer_Masked(t *testing.T) {
	truncateAll(t, testPool)
	ownerID, ownerToken := MustRegisterAndLogin(t, testRouter, "evh_h_owner")
	_, viewerToken := MustRegisterAndLogin(t, testRouter, "evh_h_viewer")

	// Share owner's default calendar with viewer
	calRepo := repository.NewCalendarRepository(testPool)
	def, err := calRepo.GetDefault(context.Background(), ownerID)
	require.NoError(t, err)

	Do(t, testRouter, "POST", fmt.Sprintf("/calendars/%s/shares", def.ID), ownerToken, map[string]string{
		"email":      "user_evh_h_viewer@example.com",
		"permission": "view",
	})

	// Owner creates a private event
	wEv := Do(t, testRouter, "POST", "/events", ownerToken, map[string]interface{}{
		"title":      "Private",
		"start_time": "2024-06-15T09:00:00Z",
		"end_time":   "2024-06-15T10:00:00Z",
		"visibility": "private",
	})
	require.Equal(t, http.StatusCreated, wEv.Code)
	var evResp struct {
		ID uuid.UUID `json:"id"`
	}
	require.NoError(t, json.NewDecoder(wEv.Body).Decode(&evResp))

	// Viewer gets masked event
	wGet := Do(t, testRouter, "GET", fmt.Sprintf("/events/%s", evResp.ID), viewerToken, nil)
	assert.Equal(t, http.StatusOK, wGet.Code)

	var got struct {
		Title     string   `json:"title"`
		Attendees []string `json:"attendees"`
	}
	require.NoError(t, json.NewDecoder(wGet.Body).Decode(&got))
	assert.Equal(t, "Busy", got.Title)
	assert.Empty(t, got.Attendees)
}

func TestEventHandler_Update_TitleOnly(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "evh_i")

	wCreate := Do(t, testRouter, "POST", "/events", token, map[string]interface{}{
		"title":      "Original",
		"start_time": "2024-06-15T09:00:00Z",
		"end_time":   "2024-06-15T10:00:00Z",
	})
	require.Equal(t, http.StatusCreated, wCreate.Code)
	var evResp struct {
		ID uuid.UUID `json:"id"`
	}
	require.NoError(t, json.NewDecoder(wCreate.Body).Decode(&evResp))

	newTitle := "Updated"
	wUp := Do(t, testRouter, "PUT", fmt.Sprintf("/events/%s", evResp.ID), token, map[string]interface{}{
		"title": newTitle,
	})
	assert.Equal(t, http.StatusOK, wUp.Code)

	var updated struct {
		Title string `json:"title"`
	}
	require.NoError(t, json.NewDecoder(wUp.Body).Decode(&updated))
	assert.Equal(t, "Updated", updated.Title)
}

func TestEventHandler_Update_ClearCategory(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "evh_cc")
	areaID := createCategory(t, token, "French", 0)

	wCreate := Do(t, testRouter, "POST", "/events", token, map[string]interface{}{
		"title":       "reading",
		"start_time":  "2024-06-15T09:00:00Z",
		"end_time":    "2024-06-15T10:00:00Z",
		"category_id": areaID,
	})
	require.Equal(t, http.StatusCreated, wCreate.Code, "body: %s", wCreate.Body.String())
	var evResp struct {
		ID         uuid.UUID  `json:"id"`
		CategoryID *uuid.UUID `json:"category_id"`
	}
	require.NoError(t, json.NewDecoder(wCreate.Body).Decode(&evResp))
	require.NotNil(t, evResp.CategoryID)

	// Omitting category_id keeps the current one.
	wKeep := Do(t, testRouter, "PUT", fmt.Sprintf("/events/%s", evResp.ID), token, map[string]interface{}{
		"title": "still reading",
	})
	require.Equal(t, http.StatusOK, wKeep.Code)
	var kept struct {
		CategoryID *uuid.UUID `json:"category_id"`
	}
	require.NoError(t, json.NewDecoder(wKeep.Body).Decode(&kept))
	assert.NotNil(t, kept.CategoryID)

	// An explicit null clears it.
	wClear := Do(t, testRouter, "PUT", fmt.Sprintf("/events/%s", evResp.ID), token, map[string]interface{}{
		"category_id": nil,
	})
	require.Equal(t, http.StatusOK, wClear.Code, "body: %s", wClear.Body.String())
	var cleared struct {
		CategoryID *uuid.UUID `json:"category_id"`
	}
	require.NoError(t, json.NewDecoder(wClear.Body).Decode(&cleared))
	assert.Nil(t, cleared.CategoryID)
}

func TestEventHandler_Update_NotFound(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "evh_j")

	newTitle := "Nope"
	w := Do(t, testRouter, "PUT", "/events/"+uuid.New().String(), token, map[string]interface{}{
		"title": newTitle,
	})
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestEventHandler_Delete(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "evh_k")

	wCreate := Do(t, testRouter, "POST", "/events", token, map[string]interface{}{
		"title":      "To Delete",
		"start_time": "2024-06-15T09:00:00Z",
		"end_time":   "2024-06-15T10:00:00Z",
	})
	require.Equal(t, http.StatusCreated, wCreate.Code)
	var evResp struct {
		ID uuid.UUID `json:"id"`
	}
	require.NoError(t, json.NewDecoder(wCreate.Body).Decode(&evResp))

	wDel := Do(t, testRouter, "DELETE", fmt.Sprintf("/events/%s", evResp.ID), token, nil)
	assert.Equal(t, http.StatusNoContent, wDel.Code)

	wGet := Do(t, testRouter, "GET", fmt.Sprintf("/events/%s", evResp.ID), token, nil)
	assert.Equal(t, http.StatusNotFound, wGet.Code)
}

func TestEventHandler_UpdateRecurrence_ScopeThis_DetachesInstance(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "evh_l")

	// Create a recurring event
	wRec := Do(t, testRouter, "POST", "/recurring-events", token, map[string]interface{}{
		"title":      "Weekly Sync",
		"start_time": "2024-01-01T09:00:00Z",
		"end_time":   "2024-01-01T10:00:00Z",
		"frequency":  "weekly",
		"interval":   1,
	})
	require.Equal(t, http.StatusCreated, wRec.Code)

	// Get one generated instance
	wList := Do(t, testRouter, "GET", "/events", token, nil)
	require.Equal(t, http.StatusOK, wList.Code)
	var instances []struct {
		ID               uuid.UUID  `json:"id"`
		RecurringEventID *uuid.UUID `json:"recurring_event_id,omitempty"`
	}
	require.NoError(t, json.NewDecoder(wList.Body).Decode(&instances))
	require.NotEmpty(t, instances)

	var instanceWithRec *struct {
		ID               uuid.UUID  `json:"id"`
		RecurringEventID *uuid.UUID `json:"recurring_event_id,omitempty"`
	}
	for i := range instances {
		if instances[i].RecurringEventID != nil {
			instanceWithRec = &instances[i]
			break
		}
	}
	require.NotNil(t, instanceWithRec, "should have a recurring instance")

	// Apply scope=this to detach this instance
	wUpd := Do(t, testRouter, "PUT", fmt.Sprintf("/events/%s/recurrence", instanceWithRec.ID), token, map[string]interface{}{
		"scope": "this",
		"title": "One-off Sync",
	})
	assert.Equal(t, http.StatusOK, wUpd.Code)

	// The instance should now have no recurring_event_id
	wGet := Do(t, testRouter, "GET", fmt.Sprintf("/events/%s", instanceWithRec.ID), token, nil)
	require.Equal(t, http.StatusOK, wGet.Code)
	var got struct {
		Title            string     `json:"title"`
		RecurringEventID *uuid.UUID `json:"recurring_event_id,omitempty"`
	}
	require.NoError(t, json.NewDecoder(wGet.Body).Decode(&got))
	assert.Equal(t, "One-off Sync", got.Title)
	assert.Nil(t, got.RecurringEventID)
}
