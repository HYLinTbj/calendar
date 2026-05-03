//go:build integration

package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/hylin/calendar/api/internal/model"
	"github.com/hylin/calendar/api/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFreeBusyHandler_Query_NotShared(t *testing.T) {
	truncateAll(t, testPool)
	_, requesterToken := MustRegisterAndLogin(t, testRouter, "fb_a_req")
	_, targetToken := MustRegisterAndLogin(t, testRouter, "fb_a_target")

	// Target creates an event (not shared with requester)
	Do(t, testRouter, "POST", "/events", targetToken, map[string]interface{}{
		"title":      "Busy Block",
		"start_time": "2024-06-15T09:00:00Z",
		"end_time":   "2024-06-15T10:00:00Z",
	})

	w := Do(t, testRouter, "GET",
		"/free-busy?emails=user_fb_a_target@example.com&from=2024-06-01T00:00:00Z&to=2024-06-30T00:00:00Z",
		requesterToken, nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp []model.FreeBusyEntry
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Len(t, resp, 1)
	assert.Equal(t, "not_shared", resp[0].Status)
}

func TestFreeBusyHandler_Query_WithShare(t *testing.T) {
	truncateAll(t, testPool)
	_, requesterToken := MustRegisterAndLogin(t, testRouter, "fb_b_req")
	targetID, targetToken := MustRegisterAndLogin(t, testRouter, "fb_b_target")

	// Target shares their default calendar with requester
	calRepo := repository.NewCalendarRepository(testPool)
	def, err := calRepo.GetDefault(context.Background(), targetID)
	require.NoError(t, err)

	Do(t, testRouter, "POST", "/calendars/"+def.ID.String()+"/shares", targetToken, map[string]string{
		"email":      "user_fb_b_req@example.com",
		"permission": "view",
	})

	// Target creates an event directly via the repo so we bypass the test router for simplicity
	evRepo := repository.NewEventRepository(testPool)
	_, err = evRepo.Create(context.Background(), targetID, def.ID, model.CreateEventRequest{
		Title:     "Busy",
		StartTime: time.Date(2024, 6, 15, 9, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2024, 6, 15, 10, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)

	w := Do(t, testRouter, "GET",
		"/free-busy?emails=user_fb_b_target@example.com&from=2024-06-01T00:00:00Z&to=2024-06-30T00:00:00Z",
		requesterToken, nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp []model.FreeBusyEntry
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Len(t, resp, 1)
	assert.Empty(t, resp[0].Status)
	assert.Len(t, resp[0].Busy, 1)
}

func TestFreeBusyHandler_Query_Self(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "fb_c")

	Do(t, testRouter, "POST", "/events", token, map[string]interface{}{
		"title":      "My Event",
		"start_time": "2024-06-15T09:00:00Z",
		"end_time":   "2024-06-15T10:00:00Z",
	})

	w := Do(t, testRouter, "GET",
		"/free-busy?emails=user_fb_c@example.com&from=2024-06-01T00:00:00Z&to=2024-06-30T00:00:00Z",
		token, nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp []model.FreeBusyEntry
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Len(t, resp, 1)
	assert.Len(t, resp[0].Busy, 1)
}

func TestFreeBusyHandler_Query_MissingParams(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "fb_d")

	// Missing from/to params
	w := Do(t, testRouter, "GET", "/free-busy?emails=someone@example.com", token, nil)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}
