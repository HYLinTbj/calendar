package repository

import (
	"testing"
	"time"

	"github.com/hylin/calendar/internal/model"
	"github.com/stretchr/testify/assert"
)

func intPtr(n int) *int { return &n }

func mustLoc(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

func TestNextOccurrences(t *testing.T) {
	anchor := time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC)
	from := anchor
	until := time.Date(2024, 1, 5, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name  string
		rule  model.RecurringEvent
		from  time.Time
		until time.Time
		want  []time.Time
	}{
		{
			name: "daily interval=1 four days",
			rule: model.RecurringEvent{
				Frequency: "daily",
				Interval:  1,
				Timezone:  "UTC",
				StartTime: anchor,
			},
			from:  from,
			until: until,
			// (from, until] → Jan 2, 3, 4, 5
			want: []time.Time{
				time.Date(2024, 1, 2, 10, 0, 0, 0, time.UTC),
				time.Date(2024, 1, 3, 10, 0, 0, 0, time.UTC),
				time.Date(2024, 1, 4, 10, 0, 0, 0, time.UTC),
				time.Date(2024, 1, 5, 10, 0, 0, 0, time.UTC),
			},
		},
		{
			name: "daily interval=2",
			rule: model.RecurringEvent{
				Frequency: "daily",
				Interval:  2,
				Timezone:  "UTC",
				StartTime: anchor,
			},
			from:  from,
			until: until,
			want: []time.Time{
				time.Date(2024, 1, 3, 10, 0, 0, 0, time.UTC),
				time.Date(2024, 1, 5, 10, 0, 0, 0, time.UTC),
			},
		},
		{
			name: "weekly no daysOfWeek interval=1",
			rule: model.RecurringEvent{
				Frequency:  "weekly",
				Interval:   1,
				DaysOfWeek: []int{},
				Timezone:   "UTC",
				StartTime:  anchor,
			},
			from:  from,
			until: time.Date(2024, 1, 29, 10, 0, 0, 0, time.UTC),
			want: []time.Time{
				time.Date(2024, 1, 8, 10, 0, 0, 0, time.UTC),
				time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC),
				time.Date(2024, 1, 22, 10, 0, 0, 0, time.UTC),
				time.Date(2024, 1, 29, 10, 0, 0, 0, time.UTC),
			},
		},
		{
			// anchor 2024-01-01 is a Monday (weekday=1)
			// daysOfWeek=[1,3] → Mon, Wed
			name: "weekly daysOfWeek Mon+Wed",
			rule: model.RecurringEvent{
				Frequency:  "weekly",
				Interval:   1,
				DaysOfWeek: []int{1, 3}, // Mon=1, Wed=3
				Timezone:   "UTC",
				StartTime:  anchor,
			},
			from:  from,
			until: time.Date(2024, 1, 10, 23, 59, 59, 0, time.UTC),
			want: []time.Time{
				time.Date(2024, 1, 3, 10, 0, 0, 0, time.UTC),  // Wed Jan 3
				time.Date(2024, 1, 8, 10, 0, 0, 0, time.UTC),  // Mon Jan 8
				time.Date(2024, 1, 10, 10, 0, 0, 0, time.UTC), // Wed Jan 10
			},
		},
		{
			// anchor is 2024-01-05 (Fri, weekday=5), interval=2 → bi-weekly Fridays
			name: "weekly daysOfWeek Fri interval=2",
			rule: model.RecurringEvent{
				Frequency:  "weekly",
				Interval:   2,
				DaysOfWeek: []int{5}, // Fri=5
				Timezone:   "UTC",
				StartTime:  time.Date(2024, 1, 5, 10, 0, 0, 0, time.UTC),
			},
			from:  time.Date(2024, 1, 5, 10, 0, 0, 0, time.UTC),
			until: time.Date(2024, 2, 2, 23, 59, 59, 0, time.UTC),
			want: []time.Time{
				time.Date(2024, 1, 19, 10, 0, 0, 0, time.UTC), // skip Jan 12 (interval=2)
				time.Date(2024, 2, 2, 10, 0, 0, 0, time.UTC),
			},
		},
		{
			name: "monthly crossing Feb",
			rule: model.RecurringEvent{
				Frequency: "monthly",
				Interval:  1,
				Timezone:  "UTC",
				StartTime: time.Date(2024, 1, 31, 10, 0, 0, 0, time.UTC),
			},
			from:  time.Date(2024, 1, 31, 10, 0, 0, 0, time.UTC),
			until: time.Date(2024, 3, 31, 10, 0, 0, 0, time.UTC),
			want: []time.Time{
				// monthly-on-31 clamps to each month's last day: Feb 29 (leap year), then back to Mar 31
				time.Date(2024, 2, 29, 10, 0, 0, 0, time.UTC),
				time.Date(2024, 3, 31, 10, 0, 0, 0, time.UTC),
			},
		},
		{
			name: "yearly leap day 2024-02-29 → 2025-02-28",
			rule: model.RecurringEvent{
				Frequency: "yearly",
				Interval:  1,
				Timezone:  "UTC",
				StartTime: time.Date(2024, 2, 29, 10, 0, 0, 0, time.UTC),
			},
			from:  time.Date(2024, 2, 29, 10, 0, 0, 0, time.UTC),
			until: time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC),
			want: []time.Time{
				// yearly from Feb 29 clamps to Feb 28 in non-leap years (returns to Feb 29 in leap years)
				time.Date(2025, 2, 28, 10, 0, 0, 0, time.UTC),
			},
		},
		{
			name: "endDate respected",
			rule: model.RecurringEvent{
				Frequency: "daily",
				Interval:  1,
				Timezone:  "UTC",
				StartTime: anchor,
				EndDate:   func() *time.Time { t := time.Date(2024, 1, 3, 10, 0, 0, 0, time.UTC); return &t }(),
			},
			from:  from,
			until: until,
			want: []time.Time{
				time.Date(2024, 1, 2, 10, 0, 0, 0, time.UTC),
				time.Date(2024, 1, 3, 10, 0, 0, 0, time.UTC),
			},
		},
		{
			// from exactly equals an occurrence → that occurrence is excluded (window is (from, until])
			name: "from equals occurrence is excluded",
			rule: model.RecurringEvent{
				Frequency: "daily",
				Interval:  1,
				Timezone:  "UTC",
				StartTime: anchor,
			},
			from:  time.Date(2024, 1, 2, 10, 0, 0, 0, time.UTC),
			until: time.Date(2024, 1, 4, 10, 0, 0, 0, time.UTC),
			want: []time.Time{
				time.Date(2024, 1, 3, 10, 0, 0, 0, time.UTC),
				time.Date(2024, 1, 4, 10, 0, 0, 0, time.UTC),
			},
		},
		{
			name: "invalid timezone falls back to UTC",
			rule: model.RecurringEvent{
				Frequency: "daily",
				Interval:  1,
				Timezone:  "Not/AReal/Timezone",
				StartTime: anchor,
			},
			from:  from,
			until: time.Date(2024, 1, 2, 10, 0, 0, 0, time.UTC),
			want: []time.Time{
				time.Date(2024, 1, 2, 10, 0, 0, 0, time.UTC),
			},
		},
		{
			// DST spring-forward: 2024-03-10 clocks go from EST→EDT at 2:00 AM
			// Event at 10:00 on Mar 9 (EST, UTC-5) = 15:00 UTC
			// Next occurrence: 10:00 on Mar 10 (EDT, UTC-4) = 14:00 UTC
			// Wall clock is preserved at 10:00 AM, UTC offset changes.
			name: "DST spring-forward wall-clock preserved",
			rule: model.RecurringEvent{
				Frequency: "daily",
				Interval:  1,
				Timezone:  "America/New_York",
				StartTime: time.Date(2024, 3, 9, 15, 0, 0, 0, time.UTC), // 10:00 EST
			},
			from:  time.Date(2024, 3, 9, 15, 0, 0, 0, time.UTC),
			until: time.Date(2024, 3, 10, 15, 0, 0, 0, time.UTC),
			want: []time.Time{
				time.Date(2024, 3, 10, 14, 0, 0, 0, time.UTC), // 10:00 EDT = UTC-4
			},
		},
		{
			name: "max_occurrences caps the series, counting the anchor as #1",
			rule: model.RecurringEvent{
				Frequency:      "daily",
				Interval:       1,
				Timezone:       "UTC",
				StartTime:      anchor,
				MaxOccurrences: intPtr(3),
			},
			from:  anchor.Add(-time.Microsecond),
			until: until,
			want: []time.Time{
				anchor,
				time.Date(2024, 1, 2, 10, 0, 0, 0, time.UTC),
				time.Date(2024, 1, 3, 10, 0, 0, 0, time.UTC),
			},
		},
		{
			name: "max_occurrences counts occurrences before from",
			rule: model.RecurringEvent{
				Frequency:      "daily",
				Interval:       1,
				Timezone:       "UTC",
				StartTime:      anchor,
				MaxOccurrences: intPtr(3),
			},
			// #1 and #2 are already generated; only #3 remains.
			from:  time.Date(2024, 1, 2, 10, 0, 0, 0, time.UTC),
			until: until,
			want: []time.Time{
				time.Date(2024, 1, 3, 10, 0, 0, 0, time.UTC),
			},
		},
		{
			name: "exdates are skipped but still count toward max_occurrences",
			rule: model.RecurringEvent{
				Frequency:      "daily",
				Interval:       1,
				Timezone:       "UTC",
				StartTime:      anchor,
				MaxOccurrences: intPtr(4),
				Exdates:        []time.Time{time.Date(2024, 1, 2, 10, 0, 0, 0, time.UTC)},
			},
			from:  anchor.Add(-time.Microsecond),
			until: until,
			want: []time.Time{
				anchor,
				time.Date(2024, 1, 3, 10, 0, 0, 0, time.UTC),
				time.Date(2024, 1, 4, 10, 0, 0, 0, time.UTC),
			},
		},
		{
			name: "all-day weekly steps in UTC regardless of timezone",
			rule: model.RecurringEvent{
				Frequency:  "weekly",
				Interval:   1,
				DaysOfWeek: []int{1},
				AllDay:     true,
				Timezone:   "America/Los_Angeles",
				StartTime:  time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC), // Monday, UTC midnight
			},
			from:  time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC),
			until: time.Date(2026, 3, 16, 0, 0, 0, 0, time.UTC),
			// Stepping in LA would land on Tuesday 00:00Z, then drift to 23:00Z after DST (Mar 8).
			want: []time.Time{
				time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC),
				time.Date(2026, 3, 16, 0, 0, 0, 0, time.UTC),
			},
		},
		{
			name: "from >= until returns nil",
			rule: model.RecurringEvent{
				Frequency: "daily",
				Interval:  1,
				Timezone:  "UTC",
				StartTime: anchor,
			},
			from:  until,
			until: from,
			want:  nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := nextOccurrences(&tc.rule, tc.from, tc.until)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestRemainingOccurrences(t *testing.T) {
	anchor := time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC)
	rule := model.RecurringEvent{Frequency: "daily", Interval: 1, Timezone: "UTC", StartTime: anchor, MaxOccurrences: intPtr(5)}

	// Forking at #3 leaves #3-#5.
	assert.Equal(t, intPtr(3), remainingOccurrences(&rule, anchor.AddDate(0, 0, 2)))
	// Forking at the anchor keeps the whole budget.
	assert.Equal(t, intPtr(5), remainingOccurrences(&rule, anchor))
	// Never below 1, even for a pivot past the last occurrence.
	assert.Equal(t, intPtr(1), remainingOccurrences(&rule, anchor.AddDate(0, 0, 30)))

	uncapped := rule
	uncapped.MaxOccurrences = nil
	assert.Nil(t, remainingOccurrences(&uncapped, anchor.AddDate(0, 0, 2)))
}

func TestRemapExdates(t *testing.T) {
	ny := "America/New_York"

	t.Run("anchor moved across DST keeps the exclusion on its occurrence", func(t *testing.T) {
		// Weekly Fri 9:00 NY from Oct 2; #5 (Fri Oct 30, EDT) excluded. Moving the series to
		// Mon Oct 5 is +72h, but #5 is then Mon Nov 2 9:00 EST — 73h later.
		from := model.RecurringEvent{Frequency: "weekly", Interval: 1, Timezone: ny,
			StartTime: time.Date(2026, 10, 2, 13, 0, 0, 0, time.UTC),
			Exdates:   []time.Time{time.Date(2026, 10, 30, 13, 0, 0, 0, time.UTC)}}
		to := from
		to.StartTime = time.Date(2026, 10, 5, 13, 0, 0, 0, time.UTC)
		assert.Equal(t, []time.Time{time.Date(2026, 11, 2, 14, 0, 0, 0, time.UTC)}, remapExdates(&from, &to, 0))
	})

	t.Run("anchor moved off fixed weekdays keeps the exclusion on its weekday", func(t *testing.T) {
		// Weekly on Mondays; Oct 12 (#3) excluded; the anchor moves to Tuesday but the
		// series still recurs on Mondays, so #3 is still Mon Oct 12.
		from := model.RecurringEvent{Frequency: "weekly", Interval: 1, DaysOfWeek: []int{1}, Timezone: ny,
			StartTime: time.Date(2026, 9, 28, 13, 0, 0, 0, time.UTC),
			Exdates:   []time.Time{time.Date(2026, 10, 12, 13, 0, 0, 0, time.UTC)}}
		to := from
		to.StartTime = time.Date(2026, 9, 29, 13, 0, 0, 0, time.UTC)
		assert.Equal(t, []time.Time{time.Date(2026, 10, 12, 13, 0, 0, 0, time.UTC)}, remapExdates(&from, &to, 0))
	})

	t.Run("switching to all-day keeps the exclusion on its date", func(t *testing.T) {
		from := model.RecurringEvent{Frequency: "daily", Interval: 1, Timezone: ny,
			StartTime: time.Date(2026, 11, 5, 14, 0, 0, 0, time.UTC), // 9:00 EST
			Exdates:   []time.Time{time.Date(2026, 11, 10, 14, 0, 0, 0, time.UTC)}}
		to := from
		to.AllDay = true
		to.StartTime = time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
		assert.Equal(t, []time.Time{time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)}, remapExdates(&from, &to, 0))
	})

	t.Run("a fork takes the exclusions from its first occurrence on", func(t *testing.T) {
		anchor := time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC)
		from := model.RecurringEvent{Frequency: "daily", Interval: 1, Timezone: "UTC", StartTime: anchor,
			Exdates: []time.Time{anchor.AddDate(0, 0, 1), anchor.AddDate(0, 0, 4)}} // #2, #5
		// Forked at #3 and moved an hour later: #5 is the fork's #3; #2 stays behind.
		fork := model.RecurringEvent{Frequency: "daily", Interval: 1, Timezone: "UTC", StartTime: anchor.AddDate(0, 0, 2).Add(time.Hour)}
		assert.Equal(t, []time.Time{anchor.AddDate(0, 0, 4).Add(time.Hour)}, remapExdates(&from, &fork, 2))
	})

	t.Run("times that aren't occurrences are dropped", func(t *testing.T) {
		from := model.RecurringEvent{Frequency: "daily", Interval: 1, Timezone: "UTC",
			StartTime: time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC),
			Exdates:   []time.Time{time.Date(2024, 1, 3, 11, 0, 0, 0, time.UTC)}}
		assert.Empty(t, remapExdates(&from, &from, 0))
	})
}

func TestStep(t *testing.T) {
	nyLoc := mustLoc("America/New_York")

	t.Run("weekly daysOfWeek picks next matching day within interval window", func(t *testing.T) {
		rule := model.RecurringEvent{
			Frequency:  "weekly",
			Interval:   1,
			DaysOfWeek: []int{1, 3}, // Mon, Wed
			Timezone:   "UTC",
		}
		// t = Monday Jan 8 10:00 UTC; next should be Wednesday Jan 10
		curr := time.Date(2024, 1, 8, 10, 0, 0, 0, time.UTC)
		result := step(&rule, curr, time.UTC, 10, 0, 0, 0)
		assert.Equal(t, time.Date(2024, 1, 10, 10, 0, 0, 0, time.UTC), result)
	})

	t.Run("weekly interval=2 skips to next week group", func(t *testing.T) {
		rule := model.RecurringEvent{
			Frequency:  "weekly",
			Interval:   2,
			DaysOfWeek: []int{5}, // Fri
			Timezone:   "UTC",
		}
		// t = Friday Jan 5; next Fri in 2-week window is Jan 19 (not Jan 12)
		curr := time.Date(2024, 1, 5, 10, 0, 0, 0, time.UTC)
		result := step(&rule, curr, time.UTC, 10, 0, 0, 0)
		assert.Equal(t, time.Date(2024, 1, 19, 10, 0, 0, 0, time.UTC), result)
	})

	t.Run("daily DST UTC offset changes but wall clock preserved", func(t *testing.T) {
		rule := model.RecurringEvent{Frequency: "daily", Interval: 1, Timezone: "America/New_York"}
		// Mar 9 10:00 EST = 15:00 UTC
		curr := time.Date(2024, 3, 9, 15, 0, 0, 0, time.UTC)
		result := step(&rule, curr, nyLoc, 10, 0, 0, 0)
		// Mar 10 10:00 EDT = 14:00 UTC
		assert.Equal(t, time.Date(2024, 3, 10, 14, 0, 0, 0, time.UTC), result)
	})
}
