//go:build integration

package handler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/hylin/calendar/internal/model"
	"github.com/hylin/calendar/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCalendarHandler_Create(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "calh_a")

	w := Do(t, testRouter, "POST", "/calendars", token, map[string]string{
		"name":        "Work",
		"description": "Work calendar",
		"color":       "#059669",
	})
	assert.Equal(t, http.StatusCreated, w.Code)

	var resp struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "Work", resp.Name)
	assert.Equal(t, "#059669", resp.Color)

	// The colour is stored, not just echoed.
	w = Do(t, testRouter, "GET", "/calendars", token, nil)
	require.Equal(t, http.StatusOK, w.Code)
	var list []calendarResp
	require.NoError(t, json.NewDecoder(w.Body).Decode(&list))
	require.Len(t, list, 2)
	assert.Equal(t, model.DefaultCalendarColor, list[0].Color, "the default calendar from registration")
	assert.Equal(t, "#059669", list[1].Color)
}

func TestCalendarHandler_Create_Validation(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "calh_a2")

	w := Do(t, testRouter, "POST", "/calendars", token, map[string]string{"name": "  "})
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// No colour means the default one.
	w = Do(t, testRouter, "POST", "/calendars", token, map[string]string{"name": "Plain"})
	require.Equal(t, http.StatusCreated, w.Code)
	var resp calendarResp
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, model.DefaultCalendarColor, resp.Color)

	// Only #rrggbb: the UI puts the colour into CSS.
	for _, color := range []string{"banana", "url(https://tracker.example/p.png)", "#12345", "#1234567", "red"} {
		w = Do(t, testRouter, "POST", "/calendars", token, map[string]string{"name": "Bad", "color": color})
		assert.Equal(t, http.StatusBadRequest, w.Code, color)
	}
	// Trimmed and lowercased, to match the UI's swatches.
	w = Do(t, testRouter, "POST", "/calendars", token, map[string]string{"name": "Padded", "color": " #DC2626 "})
	require.Equal(t, http.StatusCreated, w.Code)
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "#dc2626", resp.Color)
}

type calendarResp struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Color     string    `json:"color"`
	IsDefault bool      `json:"is_default"`
}

func TestCalendarHandler_Update(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "calh_u")

	w := Do(t, testRouter, "POST", "/calendars", token, map[string]string{"name": "Old", "color": "#059669"})
	require.Equal(t, http.StatusCreated, w.Code)
	var cal calendarResp
	require.NoError(t, json.NewDecoder(w.Body).Decode(&cal))
	path := fmt.Sprintf("/calendars/%s", cal.ID)

	w = Do(t, testRouter, "PUT", path, token, map[string]string{"name": "Renamed", "color": "#dc2626"})
	require.Equal(t, http.StatusOK, w.Code)
	var got calendarResp
	require.NoError(t, json.NewDecoder(w.Body).Decode(&got))
	assert.Equal(t, "Renamed", got.Name)
	assert.Equal(t, "#dc2626", got.Color)
	assert.False(t, got.IsDefault)

	// Only the colour: the name stays.
	w = Do(t, testRouter, "PUT", path, token, map[string]string{"color": "#7c3aed"})
	require.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, json.NewDecoder(w.Body).Decode(&got))
	assert.Equal(t, "Renamed", got.Name)
	assert.Equal(t, "#7c3aed", got.Color)

	for _, body := range []map[string]string{{"name": " "}, {"color": ""}, {"color": "url(x)"}, {"color": "#abc"}} {
		w = Do(t, testRouter, "PUT", path, token, body)
		assert.Equal(t, http.StatusBadRequest, w.Code, "%v", body)
	}

	w = Do(t, testRouter, "PUT", path, token, map[string]string{"color": "#ABCDEF"})
	require.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, json.NewDecoder(w.Body).Decode(&got))
	assert.Equal(t, "#abcdef", got.Color)
}

func TestCalendarHandler_Update_OtherUsersCalendar(t *testing.T) {
	truncateAll(t, testPool)
	ownerID, _ := MustRegisterAndLogin(t, testRouter, "calh_uo")
	_, otherToken := MustRegisterAndLogin(t, testRouter, "calh_ux")

	def, err := repository.NewCalendarRepository(testPool).GetDefault(context.Background(), ownerID)
	require.NoError(t, err)

	w := Do(t, testRouter, "PUT", fmt.Sprintf("/calendars/%s", def.ID), otherToken, map[string]string{"name": "Mine now"})
	assert.Equal(t, http.StatusNotFound, w.Code)
	got, err := repository.NewCalendarRepository(testPool).GetByID(context.Background(), def.ID, ownerID)
	require.NoError(t, err)
	assert.Equal(t, "My Calendar", got.Name)
}

func TestCalendarHandler_Update_MakeDefault(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "calh_ud")

	w := Do(t, testRouter, "POST", "/calendars", token, map[string]string{"name": "Work"})
	require.Equal(t, http.StatusCreated, w.Code)
	var cal calendarResp
	require.NoError(t, json.NewDecoder(w.Body).Decode(&cal))

	w = Do(t, testRouter, "PUT", fmt.Sprintf("/calendars/%s", cal.ID), token, map[string]any{"is_default": true})
	require.Equal(t, http.StatusOK, w.Code)

	// List puts the new default first, and only it is the default.
	w = Do(t, testRouter, "GET", "/calendars", token, nil)
	require.Equal(t, http.StatusOK, w.Code)
	var list []calendarResp
	require.NoError(t, json.NewDecoder(w.Body).Decode(&list))
	require.Len(t, list, 2)
	assert.Equal(t, cal.ID, list[0].ID)
	assert.True(t, list[0].IsDefault)
	assert.False(t, list[1].IsDefault)

	// The old default can now be deleted.
	w = Do(t, testRouter, "DELETE", fmt.Sprintf("/calendars/%s", list[1].ID), token, nil)
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestCalendarHandler_List(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "calh_b")

	// Register creates a default calendar; create one more
	Do(t, testRouter, "POST", "/calendars", token, map[string]string{"name": "Personal"})

	w := Do(t, testRouter, "GET", "/calendars", token, nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp []map[string]interface{}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Len(t, resp, 2)
}

func TestCalendarHandler_Delete_DefaultReturnsError(t *testing.T) {
	truncateAll(t, testPool)
	userID, token := MustRegisterAndLogin(t, testRouter, "calh_c")

	calRepo := repository.NewCalendarRepository(testPool)
	def, err := calRepo.GetDefault(context.Background(), userID)
	require.NoError(t, err)

	w := Do(t, testRouter, "DELETE", fmt.Sprintf("/calendars/%s", def.ID), token, nil)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCalendarHandler_Delete_MovesEvents(t *testing.T) {
	truncateAll(t, testPool)
	userID, token := MustRegisterAndLogin(t, testRouter, "calh_d")

	// Create a second calendar
	w := Do(t, testRouter, "POST", "/calendars", token, map[string]string{"name": "Extra"})
	require.Equal(t, http.StatusCreated, w.Code)
	var calResp struct {
		ID uuid.UUID `json:"id"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&calResp))

	// Create an event in that calendar
	w2 := Do(t, testRouter, "POST", "/events", token, map[string]interface{}{
		"calendar_id": calResp.ID,
		"title":       "My Event",
		"start_time":  "2024-06-15T09:00:00Z",
		"end_time":    "2024-06-15T10:00:00Z",
	})
	require.Equal(t, http.StatusCreated, w2.Code)
	var evResp struct {
		ID uuid.UUID `json:"id"`
	}
	require.NoError(t, json.NewDecoder(w2.Body).Decode(&evResp))

	// Delete the calendar
	w3 := Do(t, testRouter, "DELETE", fmt.Sprintf("/calendars/%s", calResp.ID), token, nil)
	assert.Equal(t, http.StatusNoContent, w3.Code)

	// Event should still exist but in the default calendar
	calRepo := repository.NewCalendarRepository(testPool)
	def, err := calRepo.GetDefault(context.Background(), userID)
	require.NoError(t, err)

	evRepo := repository.NewEventRepository(testPool)
	ev, err := evRepo.GetByID(context.Background(), evResp.ID, userID)
	require.NoError(t, err)
	assert.Equal(t, def.ID, ev.CalendarID)
}

func TestCalendarHandler_GetByID_NotFound(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "calh_e")

	w := Do(t, testRouter, "GET", "/calendars/"+uuid.New().String(), token, nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestCalendarHandler_Unauthenticated(t *testing.T) {
	w := Do(t, testRouter, "GET", "/calendars", "", nil)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
