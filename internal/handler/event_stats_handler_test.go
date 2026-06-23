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

func TestEventStats_Endpoint(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "stats_h")
	areaID := createCategory(t, token, "French", 300)

	post := func(title string, start time.Time, durMin int) {
		w := Do(t, testRouter, "POST", "/events", token, map[string]any{
			"title":       title,
			"start_time":  start,
			"end_time":    start.Add(time.Duration(durMin) * time.Minute),
			"category_id": areaID,
		})
		require.Equal(t, http.StatusCreated, w.Code, "create event failed: %s", w.Body.String())
	}

	base := time.Date(2024, 6, 11, 9, 0, 0, 0, time.UTC)
	post("reading", base, 60)
	post("listening", base.AddDate(0, 0, 1), 30)

	from := time.Date(2024, 6, 10, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 6, 17, 0, 0, 0, 0, time.UTC)
	url := fmt.Sprintf("/events/stats?from=%s&to=%s",
		from.Format(time.RFC3339), to.Format(time.RFC3339))
	w := Do(t, testRouter, "GET", url, token, nil)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	var stats model.TimeStats
	require.NoError(t, json.NewDecoder(w.Body).Decode(&stats))
	require.Len(t, stats.Areas, 1)

	fr := stats.Areas[0]
	assert.Equal(t, "French", fr.AreaName)
	assert.Equal(t, 300, fr.WeeklyTargetMinutes)
	assert.Equal(t, 90, fr.TotalMinutes)
	assert.Len(t, fr.SubActivities, 2)
}

func TestEventStats_RequiresAuth(t *testing.T) {
	w := Do(t, testRouter, "GET", "/events/stats", "", nil)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
