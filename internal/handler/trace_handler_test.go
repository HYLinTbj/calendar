//go:build integration

package handler_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/hylin/calendar/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTraces_CRUD(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "trace_h")
	areaID := createCategory(t, token, "French", 180)

	tr := createTrace(t, token, map[string]any{"category_id": areaID, "day": "2026-09-30", "minutes": 15, "note": "podcast"})
	assert.Equal(t, model.Date("2026-09-30"), tr.Day)
	assert.Equal(t, &areaID, tr.CategoryID)

	w := Do(t, testRouter, "GET", "/traces?from=2026-09-28&to=2026-10-04", token, nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var list []model.Trace
	require.NoError(t, json.NewDecoder(w.Body).Decode(&list))
	require.Len(t, list, 1)
	assert.Equal(t, "podcast", list[0].Note)

	w = Do(t, testRouter, "PUT", "/traces/"+tr.ID.String(), token, map[string]any{"minutes": 20, "category_id": nil})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var upd model.Trace
	require.NoError(t, json.NewDecoder(w.Body).Decode(&upd))
	assert.Equal(t, 20, upd.Minutes)
	assert.Nil(t, upd.CategoryID)
	assert.Equal(t, "podcast", upd.Note) // absent fields are kept

	w = Do(t, testRouter, "DELETE", "/traces/"+tr.ID.String(), token, nil)
	assert.Equal(t, http.StatusNoContent, w.Code)
	w = Do(t, testRouter, "DELETE", "/traces/"+tr.ID.String(), token, nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestTraces_Validation(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "trace_val")
	_, otherToken := MustRegisterAndLogin(t, testRouter, "trace_val2")
	otherArea := createCategory(t, otherToken, "Theirs", 0)

	for name, body := range map[string]map[string]any{
		"zero minutes":   {"day": "2026-09-30", "minutes": 0},
		"too many":       {"day": "2026-09-30", "minutes": 1441},
		"bad day":        {"day": "30/09/2026", "minutes": 5},
		"year zero":      {"day": "0000-01-01", "minutes": 5},
		"no day":         {"minutes": 5},
		"another's area": {"day": "2026-09-30", "minutes": 5, "category_id": otherArea},
	} {
		w := Do(t, testRouter, "POST", "/traces", token, body)
		assert.Equal(t, http.StatusBadRequest, w.Code, "%s: %s", name, w.Body.String())
	}

	tr := createTrace(t, token, map[string]any{"day": "2026-09-30", "minutes": 5})
	w := Do(t, testRouter, "PUT", "/traces/"+tr.ID.String(), token, map[string]any{"minutes": 0})
	assert.Equal(t, http.StatusBadRequest, w.Code)

	for _, q := range []string{"", "?from=2026-09-30", "?from=2026-10-01&to=2026-09-30", "?from=2024-01-01&to=2026-01-01"} {
		w := Do(t, testRouter, "GET", "/traces"+q, token, nil)
		assert.Equal(t, http.StatusBadRequest, w.Code, "GET /traces%s", q)
	}
}

func TestTraces_OtherUsersAreNotFound(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "trace_own")
	_, otherToken := MustRegisterAndLogin(t, testRouter, "trace_own2")
	tr := createTrace(t, token, map[string]any{"day": "2026-09-30", "minutes": 5})
	path := "/traces/" + tr.ID.String()

	assert.Equal(t, http.StatusNotFound, Do(t, testRouter, "GET", path, otherToken, nil).Code)
	assert.Equal(t, http.StatusNotFound, Do(t, testRouter, "PUT", path, otherToken, map[string]any{"minutes": 9}).Code)
	assert.Equal(t, http.StatusNotFound, Do(t, testRouter, "DELETE", path, otherToken, nil).Code)
	assert.Equal(t, http.StatusOK, Do(t, testRouter, "GET", path, token, nil).Code)
}

func TestEventStats_IncludesTraces(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "stats_tr")
	areaID := createCategory(t, token, "French", 180)
	createTrace(t, token, map[string]any{"category_id": areaID, "day": "2026-09-30", "minutes": 15})

	get := func(from, to time.Time, tz string) (int, model.TimeStats) {
		url := fmt.Sprintf("/events/stats?from=%s&to=%s&tz=%s", from.Format(time.RFC3339), to.Format(time.RFC3339), tz)
		w := Do(t, testRouter, "GET", url, token, nil)
		var stats model.TimeStats
		if w.Code == http.StatusOK {
			require.NoError(t, json.NewDecoder(w.Body).Decode(&stats))
		}
		return w.Code, stats
	}
	from := time.Date(2026, 9, 28, 4, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 5, 4, 0, 0, 0, time.UTC)
	code, stats := get(from, to, "America/Toronto")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, stats.Areas, 1)
	assert.Equal(t, 15, stats.Areas[0].TraceMinutes)
	assert.Equal(t, 15, stats.Areas[0].TotalMinutes)

	code, _ = get(from, to, "Not/AZone")
	assert.Equal(t, http.StatusBadRequest, code)
}
