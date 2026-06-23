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

func TestCalendarHandler_Create(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "calh_a")

	w := Do(t, testRouter, "POST", "/calendars", token, map[string]string{
		"name":        "Work",
		"description": "Work calendar",
	})
	assert.Equal(t, http.StatusCreated, w.Code)

	var resp struct {
		Name string `json:"name"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "Work", resp.Name)
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
