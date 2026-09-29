//go:build integration

package handler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

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

	// In the future: reminders whose time has already passed aren't queued.
	start := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Minute)
	w := Do(t, testRouter, "POST", "/events", token, map[string]interface{}{
		"title":      "Reminder Test",
		"start_time": start.Format(time.RFC3339),
		"end_time":   start.Add(time.Hour).Format(time.RFC3339),
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

func TestEventHandler_CategoryAssociation(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "evh_cat")
	catID := createCategory(t, token, "French", 300)

	// Create with an owned category.
	w := Do(t, testRouter, "POST", "/events", token, map[string]interface{}{
		"title":       "reading",
		"start_time":  "2024-06-15T09:00:00Z",
		"end_time":    "2024-06-15T09:45:00Z",
		"category_id": catID,
	})
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var created struct {
		ID         uuid.UUID  `json:"id"`
		CategoryID *uuid.UUID `json:"category_id"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&created))
	require.NotNil(t, created.CategoryID)
	assert.Equal(t, catID, *created.CategoryID)

	// Omitting category_id on update leaves it unchanged.
	wUpd := Do(t, testRouter, "PUT", fmt.Sprintf("/events/%s", created.ID), token, map[string]interface{}{
		"title": "reading (edited)",
	})
	require.Equal(t, http.StatusOK, wUpd.Code, wUpd.Body.String())
	var afterOmit struct {
		CategoryID *uuid.UUID `json:"category_id"`
	}
	require.NoError(t, json.NewDecoder(wUpd.Body).Decode(&afterOmit))
	require.NotNil(t, afterOmit.CategoryID)
	assert.Equal(t, catID, *afterOmit.CategoryID)

	// An explicit null clears it.
	wClear := Do(t, testRouter, "PUT", fmt.Sprintf("/events/%s", created.ID), token, map[string]interface{}{
		"category_id": nil,
	})
	require.Equal(t, http.StatusOK, wClear.Code, wClear.Body.String())
	var afterClear struct {
		CategoryID *uuid.UUID `json:"category_id"`
	}
	require.NoError(t, json.NewDecoder(wClear.Body).Decode(&afterClear))
	assert.Nil(t, afterClear.CategoryID)

	// And it can be set again via update.
	wSet := Do(t, testRouter, "PUT", fmt.Sprintf("/events/%s", created.ID), token, map[string]interface{}{
		"category_id": catID,
	})
	require.Equal(t, http.StatusOK, wSet.Code, wSet.Body.String())
	var afterSet struct {
		CategoryID *uuid.UUID `json:"category_id"`
	}
	require.NoError(t, json.NewDecoder(wSet.Body).Decode(&afterSet))
	require.NotNil(t, afterSet.CategoryID)
	assert.Equal(t, catID, *afterSet.CategoryID)
}

func TestEventHandler_Category_InvalidRejected(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "evh_catbad")
	_, otherToken := MustRegisterAndLogin(t, testRouter, "evh_catbad_other")
	foreignCat := createCategory(t, otherToken, "Theirs", 0)

	// Nonexistent category → 400, not an FK 500.
	w := Do(t, testRouter, "POST", "/events", token, map[string]interface{}{
		"title":       "x",
		"start_time":  "2024-06-15T09:00:00Z",
		"end_time":    "2024-06-15T10:00:00Z",
		"category_id": uuid.New(),
	})
	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())

	// Another user's category → 400.
	w2 := Do(t, testRouter, "POST", "/events", token, map[string]interface{}{
		"title":       "y",
		"start_time":  "2024-06-15T09:00:00Z",
		"end_time":    "2024-06-15T10:00:00Z",
		"category_id": foreignCat,
	})
	assert.Equal(t, http.StatusBadRequest, w2.Code, w2.Body.String())

	// Same rejection on update.
	wEv := Do(t, testRouter, "POST", "/events", token, map[string]interface{}{
		"title":      "z",
		"start_time": "2024-06-15T09:00:00Z",
		"end_time":   "2024-06-15T10:00:00Z",
	})
	require.Equal(t, http.StatusCreated, wEv.Code)
	var ev struct {
		ID uuid.UUID `json:"id"`
	}
	require.NoError(t, json.NewDecoder(wEv.Body).Decode(&ev))

	wUpd := Do(t, testRouter, "PUT", fmt.Sprintf("/events/%s", ev.ID), token, map[string]interface{}{
		"category_id": foreignCat,
	})
	assert.Equal(t, http.StatusBadRequest, wUpd.Code, wUpd.Body.String())
}

func TestRecurringEventHandler_CategoryValidated(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "rec_cat")
	_, otherToken := MustRegisterAndLogin(t, testRouter, "rec_cat_other")
	ownCat := createCategory(t, token, "Study", 0)
	foreignCat := createCategory(t, otherToken, "Theirs", 0)

	base := map[string]interface{}{
		"title":      "Weekly Sync",
		"start_time": "2024-01-01T09:00:00Z",
		"end_time":   "2024-01-01T10:00:00Z",
		"frequency":  "weekly",
		"interval":   1,
	}
	with := func(catID interface{}) map[string]interface{} {
		m := map[string]interface{}{}
		for k, v := range base {
			m[k] = v
		}
		m["category_id"] = catID
		return m
	}

	// An owned category is accepted.
	wOK := Do(t, testRouter, "POST", "/recurring-events", token, with(ownCat))
	require.Equal(t, http.StatusCreated, wOK.Code, wOK.Body.String())
	var rec struct {
		ID uuid.UUID `json:"id"`
	}
	require.NoError(t, json.NewDecoder(wOK.Body).Decode(&rec))

	// Nonexistent category → 400, not an FK 500 (which would also leak into every
	// materialized instance).
	wBad := Do(t, testRouter, "POST", "/recurring-events", token, with(uuid.New()))
	assert.Equal(t, http.StatusBadRequest, wBad.Code, wBad.Body.String())

	// Another tenant's category → 400.
	wForeign := Do(t, testRouter, "POST", "/recurring-events", token, with(foreignCat))
	assert.Equal(t, http.StatusBadRequest, wForeign.Code, wForeign.Body.String())

	// Same rejection on update.
	wUpd := Do(t, testRouter, "PUT", fmt.Sprintf("/recurring-events/%s", rec.ID), token, map[string]interface{}{
		"category_id": foreignCat,
	})
	assert.Equal(t, http.StatusBadRequest, wUpd.Code, wUpd.Body.String())
}

func TestRecurringEventHandler_RejectsNonPositiveMaxOccurrences(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "rec_maxocc")

	body := func(n int) map[string]interface{} {
		return map[string]interface{}{
			"title":           "Capped",
			"start_time":      "2024-01-01T09:00:00Z",
			"end_time":        "2024-01-01T10:00:00Z",
			"frequency":       "daily",
			"max_occurrences": n,
		}
	}
	// The cap is enforced, so 0 or less would create a rule with no instances —
	// invisible in the UI and re-scanned by the scheduler forever.
	for _, n := range []int{0, -1} {
		w := Do(t, testRouter, "POST", "/recurring-events", token, body(n))
		assert.Equal(t, http.StatusBadRequest, w.Code, "max_occurrences=%d: %s", n, w.Body.String())
	}
	w := Do(t, testRouter, "POST", "/recurring-events", token, body(1))
	assert.Equal(t, http.StatusCreated, w.Code, w.Body.String())
}

func TestEventHandler_DeleteRecurrence_Scopes(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "evh_delrec")

	type inst struct {
		ID               uuid.UUID  `json:"id"`
		RecurringEventID *uuid.UUID `json:"recurring_event_id"`
	}
	list := func() []inst {
		w := Do(t, testRouter, "GET", "/events", token, nil)
		require.Equal(t, http.StatusOK, w.Code)
		var out []inst
		require.NoError(t, json.NewDecoder(w.Body).Decode(&out))
		return out
	}
	w := Do(t, testRouter, "POST", "/recurring-events", token, map[string]interface{}{
		"title":           "Daily",
		"start_time":      "2024-01-01T09:00:00Z",
		"end_time":        "2024-01-01T10:00:00Z",
		"frequency":       "daily",
		"max_occurrences": 5,
	})
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	all := list()
	require.Len(t, all, 5)

	del := func(id uuid.UUID, scope string) int {
		return Do(t, testRouter, "DELETE", fmt.Sprintf("/events/%s/recurrence?scope=%s", id, scope), token, nil).Code
	}
	assert.Equal(t, http.StatusBadRequest, del(all[0].ID, "bogus"))

	// "this" removes one; "this_and_following" from #4 removes #4-#5 and keeps the rest.
	assert.Equal(t, http.StatusNoContent, del(all[1].ID, "this"))
	assert.Len(t, list(), 4)
	assert.Equal(t, http.StatusNoContent, del(all[3].ID, "this_and_following"))
	left := list()
	require.Len(t, left, 2)
	assert.Equal(t, all[0].ID, left[0].ID)
	assert.Equal(t, all[2].ID, left[1].ID)

	// A standalone event isn't part of a series.
	wOne := Do(t, testRouter, "POST", "/events", token, map[string]interface{}{
		"title": "One-off", "start_time": "2024-02-01T09:00:00Z", "end_time": "2024-02-01T10:00:00Z",
	})
	require.Equal(t, http.StatusCreated, wOne.Code)
	var one inst
	require.NoError(t, json.NewDecoder(wOne.Body).Decode(&one))
	assert.Equal(t, http.StatusBadRequest, del(one.ID, "all"))

	// "all" removes the series and its remaining instances.
	assert.Equal(t, http.StatusNoContent, del(left[0].ID, "all"))
	remaining := list()
	require.Len(t, remaining, 1)
	assert.Equal(t, one.ID, remaining[0].ID)
}

func TestEventHandler_DeleteRecurrence_FromEditedOccurrence(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "evh_delexc")

	type inst struct {
		ID uuid.UUID `json:"id"`
	}
	list := func() []inst {
		w := Do(t, testRouter, "GET", "/events", token, nil)
		require.Equal(t, http.StatusOK, w.Code)
		var out []inst
		require.NoError(t, json.NewDecoder(w.Body).Decode(&out))
		return out
	}
	move := func(id uuid.UUID, start string) {
		w := Do(t, testRouter, "PUT", fmt.Sprintf("/events/%s", id), token, map[string]interface{}{
			"start_time": start + "T15:00:00Z", "end_time": start + "T16:00:00Z",
		})
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	}
	del := func(id uuid.UUID, scope string) int {
		return Do(t, testRouter, "DELETE", fmt.Sprintf("/events/%s/recurrence?scope=%s", id, scope), token, nil).Code
	}
	w := Do(t, testRouter, "POST", "/recurring-events", token, map[string]interface{}{
		"title": "Daily", "start_time": "2024-01-01T09:00:00Z", "end_time": "2024-01-01T10:00:00Z",
		"frequency": "daily", "max_occurrences": 5,
	})
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	all := list()
	require.Len(t, all, 5)

	// A moved occurrence is still part of the series: "this and following" from it counts
	// from the occurrence it was (#3), removing it and #4-#5.
	move(all[2].ID, "2024-01-03")
	assert.Equal(t, http.StatusNoContent, del(all[2].ID, "this_and_following"))
	left := list()
	require.Len(t, left, 2)
	assert.Equal(t, all[0].ID, left[0].ID)
	assert.Equal(t, all[1].ID, left[1].ID)

	// And "all" from one deletes the whole series.
	move(all[0].ID, "2024-01-01")
	assert.Equal(t, http.StatusNoContent, del(all[0].ID, "all"))
	assert.Empty(t, list())
}

func TestEventHandler_Update_DetachesSeriesInstance(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "evh_detach")

	w := Do(t, testRouter, "POST", "/recurring-events", token, map[string]interface{}{
		"title":           "Daily",
		"start_time":      "2024-01-01T09:00:00Z",
		"end_time":        "2024-01-01T10:00:00Z",
		"frequency":       "daily",
		"max_occurrences": 3,
	})
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var rec struct {
		ID uuid.UUID `json:"id"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&rec))

	wList := Do(t, testRouter, "GET", "/events", token, nil)
	var instances []struct {
		ID uuid.UUID `json:"id"`
	}
	require.NoError(t, json.NewDecoder(wList.Body).Decode(&instances))
	require.Len(t, instances, 3)

	// A plain PUT (drag, Log view edit) makes the instance an exception.
	wUpd := Do(t, testRouter, "PUT", fmt.Sprintf("/events/%s", instances[1].ID), token, map[string]interface{}{
		"start_time": "2024-01-02T11:00:00Z", "end_time": "2024-01-02T12:00:00Z",
	})
	require.Equal(t, http.StatusOK, wUpd.Code, wUpd.Body.String())
	var upd struct {
		RecurringEventID *uuid.UUID `json:"recurring_event_id"`
		DetachedFrom     *uuid.UUID `json:"detached_from"`
	}
	require.NoError(t, json.NewDecoder(wUpd.Body).Decode(&upd))
	assert.Nil(t, upd.RecurringEventID)
	require.NotNil(t, upd.DetachedFrom, "the exception still says which series it belongs to")
	assert.Equal(t, rec.ID, *upd.DetachedFrom)

	wRec := Do(t, testRouter, "GET", fmt.Sprintf("/recurring-events/%s", rec.ID), token, nil)
	var got struct {
		Exdates []time.Time `json:"exdates"`
	}
	require.NoError(t, json.NewDecoder(wRec.Body).Decode(&got))
	require.Len(t, got.Exdates, 1)
	assert.True(t, got.Exdates[0].Equal(time.Date(2024, 1, 2, 9, 0, 0, 0, time.UTC)), "the original occurrence time is excluded, got %v", got.Exdates[0])
}

func TestEventHandler_List_Overlap(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "evh_overlap")

	w := Do(t, testRouter, "POST", "/events", token, map[string]interface{}{
		"title": "Conference", "start_time": "2026-09-25T00:00:00Z", "end_time": "2026-09-28T23:59:59Z", "all_day": true,
	})
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	count := func(q string) int {
		w := Do(t, testRouter, "GET", "/events?from=2026-09-27T07:00:00Z&to=2026-10-04T06:59:59Z"+q, token, nil)
		require.Equal(t, http.StatusOK, w.Code)
		var evs []map[string]any
		require.NoError(t, json.NewDecoder(w.Body).Decode(&evs))
		return len(evs)
	}
	assert.Equal(t, 0, count(""), "by default only events starting in range")
	assert.Equal(t, 1, count("&overlap=true"), "a multi-day event still running at the start of the range")
}

func TestEventHandler_List_GeneratesOccurrencesAhead(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "evh_ahead")

	start := time.Now().UTC().Truncate(time.Hour).Add(time.Hour)
	w := Do(t, testRouter, "POST", "/recurring-events", token, map[string]interface{}{
		"title": "Weekly", "start_time": start.Format(time.RFC3339), "end_time": start.Add(time.Hour).Format(time.RFC3339),
		"frequency": "weekly",
	})
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	// A month about five months out — past the 60-day materialized window.
	from, to := start.AddDate(0, 5, 0), start.AddDate(0, 6, 0)
	wList := Do(t, testRouter, "GET", fmt.Sprintf("/events?from=%s&to=%s&overlap=true",
		from.Format(time.RFC3339), to.Format(time.RFC3339)), token, nil)
	require.Equal(t, http.StatusOK, wList.Code)
	var evs []map[string]any
	require.NoError(t, json.NewDecoder(wList.Body).Decode(&evs))
	assert.GreaterOrEqual(t, len(evs), 4, "the series' occurrences in that month are generated on demand")
}
