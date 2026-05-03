//go:build integration

package handler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func getFirstInvitationToken(t *testing.T, eventID uuid.UUID) uuid.UUID {
	t.Helper()
	var tokenStr string
	err := testPool.QueryRow(context.Background(),
		`SELECT token::text FROM event_invitations WHERE event_id=$1 LIMIT 1`, eventID).
		Scan(&tokenStr)
	require.NoError(t, err)
	token, err := uuid.Parse(tokenStr)
	require.NoError(t, err)
	return token
}

func getInvitationStatus(t *testing.T, eventID uuid.UUID, email string) string {
	t.Helper()
	var status string
	err := testPool.QueryRow(context.Background(),
		`SELECT status FROM event_invitations WHERE event_id=$1 AND email=$2`,
		eventID, email).Scan(&status)
	require.NoError(t, err)
	return status
}

func TestInvitationHandler_Accept(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "invh_a")

	wEv := Do(t, testRouter, "POST", "/events", token, map[string]interface{}{
		"title":      "Team Meeting",
		"start_time": "2024-06-15T09:00:00Z",
		"end_time":   "2024-06-15T10:00:00Z",
		"attendees":  []string{"alice@example.com"},
	})
	require.Equal(t, http.StatusCreated, wEv.Code)
	var evResp struct {
		ID uuid.UUID `json:"id"`
	}
	require.NoError(t, json.NewDecoder(wEv.Body).Decode(&evResp))

	invToken := getFirstInvitationToken(t, evResp.ID)
	w := Do(t, testRouter, "GET", fmt.Sprintf("/invitations/%s/accept", invToken), "", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	assert.Equal(t, "accepted", getInvitationStatus(t, evResp.ID, "alice@example.com"))
}

func TestInvitationHandler_Decline(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "invh_b")

	wEv := Do(t, testRouter, "POST", "/events", token, map[string]interface{}{
		"title":      "Team Meeting",
		"start_time": "2024-06-15T09:00:00Z",
		"end_time":   "2024-06-15T10:00:00Z",
		"attendees":  []string{"bob@example.com"},
	})
	require.Equal(t, http.StatusCreated, wEv.Code)
	var evResp struct {
		ID uuid.UUID `json:"id"`
	}
	require.NoError(t, json.NewDecoder(wEv.Body).Decode(&evResp))

	invToken := getFirstInvitationToken(t, evResp.ID)
	w := Do(t, testRouter, "GET", fmt.Sprintf("/invitations/%s/decline", invToken), "", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	assert.Equal(t, "declined", getInvitationStatus(t, evResp.ID, "bob@example.com"))
}

func TestInvitationHandler_Tentative(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "invh_c")

	wEv := Do(t, testRouter, "POST", "/events", token, map[string]interface{}{
		"title":      "Team Meeting",
		"start_time": "2024-06-15T09:00:00Z",
		"end_time":   "2024-06-15T10:00:00Z",
		"attendees":  []string{"carol@example.com"},
	})
	require.Equal(t, http.StatusCreated, wEv.Code)
	var evResp struct {
		ID uuid.UUID `json:"id"`
	}
	require.NoError(t, json.NewDecoder(wEv.Body).Decode(&evResp))

	invToken := getFirstInvitationToken(t, evResp.ID)
	w := Do(t, testRouter, "GET", fmt.Sprintf("/invitations/%s/tentative", invToken), "", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	assert.Equal(t, "tentative", getInvitationStatus(t, evResp.ID, "carol@example.com"))
}

func TestInvitationHandler_Rerespond_ChangesStatus(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "invh_d")

	wEv := Do(t, testRouter, "POST", "/events", token, map[string]interface{}{
		"title":      "Team Meeting",
		"start_time": "2024-06-15T09:00:00Z",
		"end_time":   "2024-06-15T10:00:00Z",
		"attendees":  []string{"dave@example.com"},
	})
	require.Equal(t, http.StatusCreated, wEv.Code)
	var evResp struct {
		ID uuid.UUID `json:"id"`
	}
	require.NoError(t, json.NewDecoder(wEv.Body).Decode(&evResp))

	invToken := getFirstInvitationToken(t, evResp.ID)

	// First, decline
	Do(t, testRouter, "GET", fmt.Sprintf("/invitations/%s/decline", invToken), "", nil)
	assert.Equal(t, "declined", getInvitationStatus(t, evResp.ID, "dave@example.com"))

	// Then accept (change of mind)
	Do(t, testRouter, "GET", fmt.Sprintf("/invitations/%s/accept", invToken), "", nil)
	assert.Equal(t, "accepted", getInvitationStatus(t, evResp.ID, "dave@example.com"))
}

func TestInvitationHandler_InvalidToken_NotFound(t *testing.T) {
	w := Do(t, testRouter, "GET", fmt.Sprintf("/invitations/%s/accept", uuid.New()), "", nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
}
