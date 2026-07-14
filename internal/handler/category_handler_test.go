//go:build integration

package handler_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCategoryHandler_CRUD(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "cat_crud")

	w := Do(t, testRouter, "POST", "/categories", token, map[string]any{
		"name": "French", "color": "#4f46e5", "weekly_target_minutes": 300,
	})
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var cat struct {
		ID                  uuid.UUID `json:"id"`
		Name                string    `json:"name"`
		Color               string    `json:"color"`
		WeeklyTargetMinutes int       `json:"weekly_target_minutes"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&cat))
	assert.Equal(t, "French", cat.Name)
	assert.Equal(t, 300, cat.WeeklyTargetMinutes)

	// Duplicate name for the same owner → 409.
	wDup := Do(t, testRouter, "POST", "/categories", token, map[string]any{
		"name": "French", "color": "#000000",
	})
	assert.Equal(t, http.StatusConflict, wDup.Code, wDup.Body.String())

	wList := Do(t, testRouter, "GET", "/categories", token, nil)
	require.Equal(t, http.StatusOK, wList.Code)
	var cats []struct {
		ID uuid.UUID `json:"id"`
	}
	require.NoError(t, json.NewDecoder(wList.Body).Decode(&cats))
	assert.Len(t, cats, 1)

	// Partial update: untouched fields keep their values.
	wUpd := Do(t, testRouter, "PUT", fmt.Sprintf("/categories/%s", cat.ID), token, map[string]any{
		"name": "Français", "weekly_target_minutes": 240,
	})
	require.Equal(t, http.StatusOK, wUpd.Code, wUpd.Body.String())
	var upd struct {
		Name                string `json:"name"`
		Color               string `json:"color"`
		WeeklyTargetMinutes int    `json:"weekly_target_minutes"`
	}
	require.NoError(t, json.NewDecoder(wUpd.Body).Decode(&upd))
	assert.Equal(t, "Français", upd.Name)
	assert.Equal(t, "#4f46e5", upd.Color)
	assert.Equal(t, 240, upd.WeeklyTargetMinutes)

	wDel := Do(t, testRouter, "DELETE", fmt.Sprintf("/categories/%s", cat.ID), token, nil)
	assert.Equal(t, http.StatusNoContent, wDel.Code)
	wGet := Do(t, testRouter, "GET", fmt.Sprintf("/categories/%s", cat.ID), token, nil)
	assert.Equal(t, http.StatusNotFound, wGet.Code)
}

func TestCategoryHandler_DeleteUncategorizesEvents(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "cat_del")
	catID := createCategory(t, token, "Fitness", 0)

	wEv := Do(t, testRouter, "POST", "/events", token, map[string]any{
		"title":       "strength",
		"start_time":  "2024-06-15T09:00:00Z",
		"end_time":    "2024-06-15T10:00:00Z",
		"category_id": catID,
	})
	require.Equal(t, http.StatusCreated, wEv.Code, wEv.Body.String())
	var ev struct {
		ID uuid.UUID `json:"id"`
	}
	require.NoError(t, json.NewDecoder(wEv.Body).Decode(&ev))

	wDel := Do(t, testRouter, "DELETE", fmt.Sprintf("/categories/%s", catID), token, nil)
	require.Equal(t, http.StatusNoContent, wDel.Code)

	// The event survives with its category cleared (FK ON DELETE SET NULL).
	wGet := Do(t, testRouter, "GET", fmt.Sprintf("/events/%s", ev.ID), token, nil)
	require.Equal(t, http.StatusOK, wGet.Code)
	var got struct {
		Title      string     `json:"title"`
		CategoryID *uuid.UUID `json:"category_id"`
	}
	require.NoError(t, json.NewDecoder(wGet.Body).Decode(&got))
	assert.Equal(t, "strength", got.Title)
	assert.Nil(t, got.CategoryID)
}

func TestCategoryHandler_TenantIsolation(t *testing.T) {
	truncateAll(t, testPool)
	_, tokenA := MustRegisterAndLogin(t, testRouter, "cat_iso_a")
	_, tokenB := MustRegisterAndLogin(t, testRouter, "cat_iso_b")
	catA := createCategory(t, tokenA, "Private", 0)

	// B cannot see, update, or list A's category.
	wGet := Do(t, testRouter, "GET", fmt.Sprintf("/categories/%s", catA), tokenB, nil)
	assert.Equal(t, http.StatusNotFound, wGet.Code)
	wUpd := Do(t, testRouter, "PUT", fmt.Sprintf("/categories/%s", catA), tokenB, map[string]any{"name": "Hijacked"})
	assert.Equal(t, http.StatusNotFound, wUpd.Code)
	wList := Do(t, testRouter, "GET", "/categories", tokenB, nil)
	require.Equal(t, http.StatusOK, wList.Code)
	var cats []struct {
		ID uuid.UUID `json:"id"`
	}
	require.NoError(t, json.NewDecoder(wList.Body).Decode(&cats))
	assert.Empty(t, cats)
}
