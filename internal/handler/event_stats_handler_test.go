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

func TestWeeklyStats_Endpoint(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "weekly_h")
	areaID := createCategory(t, token, "French", 120)

	start := time.Date(2026, 9, 29, 13, 0, 0, 0, time.UTC) // Tuesday
	w := Do(t, testRouter, "POST", "/events", token, map[string]any{
		"title": "class", "start_time": start, "end_time": start.Add(45 * time.Minute), "category_id": areaID,
	})
	require.Equal(t, http.StatusCreated, w.Code, "create event failed: %s", w.Body.String())
	createTrace(t, token, map[string]any{"category_id": areaID, "day": "2026-10-04", "minutes": 15}) // Sunday

	// Two Sunday weeks in Toronto.
	from := time.Date(2026, 9, 27, 4, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 11, 4, 0, 0, 0, time.UTC)
	url := fmt.Sprintf("/events/stats/weekly?from=%s&to=%s&tz=America/Toronto&week_start=0",
		from.Format(time.RFC3339), to.Format(time.RFC3339))
	w = Do(t, testRouter, "GET", url, token, nil)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	var stats model.WeeklyStats
	require.NoError(t, json.NewDecoder(w.Body).Decode(&stats))
	assert.Equal(t, []model.Date{"2026-09-27", "2026-10-04"}, stats.Weeks)
	assert.Equal(t, 0, stats.WeekStart)
	require.Len(t, stats.Areas, 1)
	assert.Equal(t, "French", stats.Areas[0].AreaName)
	assert.Equal(t, 120, stats.Areas[0].WeeklyTargetMinutes)
	assert.Equal(t, []int{45, 15}, stats.Areas[0].Minutes)

	// Defaults: Monday weeks, UTC, the 8 weeks before now.
	w = Do(t, testRouter, "GET", "/events/stats/weekly", token, nil)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	stats = model.WeeklyStats{}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&stats))
	assert.Equal(t, 1, stats.WeekStart)
	assert.Equal(t, "UTC", stats.TZ)
	assert.Len(t, stats.Weeks, 9) // 56 days touch 9 weeks unless they start on a Monday midnight
	first, err := model.ParseDate(string(stats.Weeks[0]))
	require.NoError(t, err)
	assert.Equal(t, time.Monday, first.Weekday())
}

func TestWeeklyStats_Validation(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "weekly_v")

	from := time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)
	rng := func(from, to time.Time) string {
		return fmt.Sprintf("from=%s&to=%s", from.Format(time.RFC3339), to.Format(time.RFC3339))
	}
	ok := rng(from, from.AddDate(0, 0, 14))
	for name, query := range map[string]string{
		"week_start too big": ok + "&week_start=7",
		"week_start < 0":     ok + "&week_start=-1",
		"week_start word":    ok + "&week_start=sunday",
		"bad tz":             ok + "&tz=Not/AZone",
		"empty range":        rng(from, from),
		"from after to":      rng(from, from.AddDate(0, 0, -7)),
		"over 53 weeks":      rng(from, from.AddDate(0, 0, 53*7+1)) + "&week_start=0",
		"54 weeks touched":   rng(from, from.AddDate(0, 0, 53*7)), // from is a Sunday: Monday weeks
		"bad from":           "from=yesterday",
	} {
		w := Do(t, testRouter, "GET", "/events/stats/weekly?"+query, token, nil)
		assert.Equal(t, http.StatusBadRequest, w.Code, "%s: %s", name, w.Body.String())
	}

	w := Do(t, testRouter, "GET", "/events/stats/weekly?"+rng(from, from.AddDate(0, 0, 53*7))+"&week_start=0", token, nil)
	assert.Equal(t, http.StatusOK, w.Code, "53 weeks: %s", w.Body.String())
	// 53 local weeks across three DST changes are an hour over 53×7 days.
	toronto := rng(time.Date(2025, 11, 2, 4, 0, 0, 0, time.UTC), time.Date(2026, 11, 8, 5, 0, 0, 0, time.UTC))
	w = Do(t, testRouter, "GET", "/events/stats/weekly?"+toronto+"&tz=America/Toronto&week_start=0", token, nil)
	assert.Equal(t, http.StatusOK, w.Code, "53 Toronto weeks: %s", w.Body.String())

	w = Do(t, testRouter, "GET", "/events/stats/weekly", "", nil)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
