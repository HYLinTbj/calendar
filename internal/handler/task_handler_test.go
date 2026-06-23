//go:build integration

package handler_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/hylin/calendar/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTask_CreateListToggle(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "th_a")
	areaID := createCategory(t, token, "Fitness", 0)

	w := Do(t, testRouter, "POST", "/tasks", token, map[string]any{
		"area_id": areaID,
		"title":   "Sign up for gym",
	})
	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())
	var task model.Task
	require.NoError(t, json.NewDecoder(w.Body).Decode(&task))
	assert.False(t, task.Done)

	// List open tasks scoped to the area.
	w = Do(t, testRouter, "GET", fmt.Sprintf("/tasks?area_id=%s&done=false", areaID), token, nil)
	require.Equal(t, http.StatusOK, w.Code)
	var open []model.Task
	require.NoError(t, json.NewDecoder(w.Body).Decode(&open))
	require.Len(t, open, 1)

	// Toggle done -> completed_at populated.
	w = Do(t, testRouter, "PUT", "/tasks/"+task.ID.String(), token, map[string]any{"done": true})
	require.Equal(t, http.StatusOK, w.Code)
	var updated model.Task
	require.NoError(t, json.NewDecoder(w.Body).Decode(&updated))
	assert.True(t, updated.Done)
	require.NotNil(t, updated.CompletedAt)

	// It no longer appears among open tasks.
	w = Do(t, testRouter, "GET", "/tasks?done=false", token, nil)
	require.Equal(t, http.StatusOK, w.Code)
	var stillOpen []model.Task
	require.NoError(t, json.NewDecoder(w.Body).Decode(&stillOpen))
	assert.Len(t, stillOpen, 0)
}

func TestTask_RequiresTitle(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "th_b")

	w := Do(t, testRouter, "POST", "/tasks", token, map[string]any{"notes": "no title"})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestTask_DeleteAndRequireAuth(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "th_c")

	w := Do(t, testRouter, "POST", "/tasks", token, map[string]any{"title": "ephemeral"})
	require.Equal(t, http.StatusCreated, w.Code)
	var task model.Task
	require.NoError(t, json.NewDecoder(w.Body).Decode(&task))

	w = Do(t, testRouter, "DELETE", "/tasks/"+task.ID.String(), token, nil)
	require.Equal(t, http.StatusNoContent, w.Code)

	w = Do(t, testRouter, "GET", "/tasks/"+task.ID.String(), token, nil)
	assert.Equal(t, http.StatusNotFound, w.Code)

	// No token at all.
	w = Do(t, testRouter, "GET", "/tasks", "", nil)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
