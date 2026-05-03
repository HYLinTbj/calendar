package worker

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func mustLoc(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

func intPtr(n int) *int { return &n }

func TestNextOccurrences(t *testing.T) {
	anchor := time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC)
	from := anchor
	until := time.Date(2024, 1, 5, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name  string
		rule  recurringRule
		from  time.Time
		until time.Time
		want  []time.Time
	}{
		{
			name: "daily interval=1 four days",
			rule: recurringRule{
				frequency: "daily",
				interval:  1,
				timezone:  "UTC",
				startTime: anchor,
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
			rule: recurringRule{
				frequency: "daily",
				interval:  2,
				timezone:  "UTC",
				startTime: anchor,
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
			rule: recurringRule{
				frequency:  "weekly",
				interval:   1,
				daysOfWeek: []int{},
				timezone:   "UTC",
				startTime:  anchor,
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
			rule: recurringRule{
				frequency:  "weekly",
				interval:   1,
				daysOfWeek: []int{1, 3}, // Mon=1, Wed=3
				timezone:   "UTC",
				startTime:  anchor,
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
			rule: recurringRule{
				frequency:  "weekly",
				interval:   2,
				daysOfWeek: []int{5}, // Fri=5
				timezone:   "UTC",
				startTime:  time.Date(2024, 1, 5, 10, 0, 0, 0, time.UTC),
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
			rule: recurringRule{
				frequency: "monthly",
				interval:  1,
				timezone:  "UTC",
				startTime: time.Date(2024, 1, 31, 10, 0, 0, 0, time.UTC),
			},
			from:  time.Date(2024, 1, 31, 10, 0, 0, 0, time.UTC),
			until: time.Date(2024, 3, 31, 10, 0, 0, 0, time.UTC),
			want: []time.Time{
				// 2024 is a leap year: Jan 31 +1 month = Feb 29 (Go's AddDate behavior)
				time.Date(2024, 2, 29, 10, 0, 0, 0, time.UTC),
				time.Date(2024, 3, 31, 10, 0, 0, 0, time.UTC),
			},
		},
		{
			name: "yearly leap day 2024-02-29 → 2025-02-28",
			rule: recurringRule{
				frequency: "yearly",
				interval:  1,
				timezone:  "UTC",
				startTime: time.Date(2024, 2, 29, 10, 0, 0, 0, time.UTC),
			},
			from:  time.Date(2024, 2, 29, 10, 0, 0, 0, time.UTC),
			until: time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC),
			want: []time.Time{
				// AddDate(1,0,0) from Feb 29, 2024 → Feb 28, 2025 (Go normalizes)
				time.Date(2025, 2, 28, 10, 0, 0, 0, time.UTC),
			},
		},
		{
			name: "endDate respected",
			rule: recurringRule{
				frequency: "daily",
				interval:  1,
				timezone:  "UTC",
				startTime: anchor,
				endDate:   func() *time.Time { t := time.Date(2024, 1, 3, 10, 0, 0, 0, time.UTC); return &t }(),
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
			rule: recurringRule{
				frequency: "daily",
				interval:  1,
				timezone:  "UTC",
				startTime: anchor,
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
			rule: recurringRule{
				frequency: "daily",
				interval:  1,
				timezone:  "Not/AReal/Timezone",
				startTime: anchor,
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
			rule: recurringRule{
				frequency: "daily",
				interval:  1,
				timezone:  "America/New_York",
				startTime: time.Date(2024, 3, 9, 15, 0, 0, 0, time.UTC), // 10:00 EST
			},
			from:  time.Date(2024, 3, 9, 15, 0, 0, 0, time.UTC),
			until: time.Date(2024, 3, 10, 15, 0, 0, 0, time.UTC),
			want: []time.Time{
				time.Date(2024, 3, 10, 14, 0, 0, 0, time.UTC), // 10:00 EDT = UTC-4
			},
		},
		{
			name: "from >= until returns nil",
			rule: recurringRule{
				frequency: "daily",
				interval:  1,
				timezone:  "UTC",
				startTime: anchor,
			},
			from:  until,
			until: from,
			want:  nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := nextOccurrences(tc.rule, tc.from, tc.until)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestStep(t *testing.T) {
	nyLoc := mustLoc("America/New_York")

	t.Run("weekly daysOfWeek picks next matching day within interval window", func(t *testing.T) {
		rule := recurringRule{
			frequency:  "weekly",
			interval:   1,
			daysOfWeek: []int{1, 3}, // Mon, Wed
			timezone:   "UTC",
		}
		// t = Monday Jan 8 10:00 UTC; next should be Wednesday Jan 10
		curr := time.Date(2024, 1, 8, 10, 0, 0, 0, time.UTC)
		result := step(rule, curr, time.UTC, 10, 0, 0, 0)
		assert.Equal(t, time.Date(2024, 1, 10, 10, 0, 0, 0, time.UTC), result)
	})

	t.Run("weekly interval=2 skips to next week group", func(t *testing.T) {
		rule := recurringRule{
			frequency:  "weekly",
			interval:   2,
			daysOfWeek: []int{5}, // Fri
			timezone:   "UTC",
		}
		// t = Friday Jan 5; next Fri in 2-week window is Jan 19 (not Jan 12)
		curr := time.Date(2024, 1, 5, 10, 0, 0, 0, time.UTC)
		result := step(rule, curr, time.UTC, 10, 0, 0, 0)
		assert.Equal(t, time.Date(2024, 1, 19, 10, 0, 0, 0, time.UTC), result)
	})

	t.Run("daily DST UTC offset changes but wall clock preserved", func(t *testing.T) {
		rule := recurringRule{frequency: "daily", interval: 1, timezone: "America/New_York"}
		// Mar 9 10:00 EST = 15:00 UTC
		curr := time.Date(2024, 3, 9, 15, 0, 0, 0, time.UTC)
		result := step(rule, curr, nyLoc, 10, 0, 0, 0)
		// Mar 10 10:00 EDT = 14:00 UTC
		assert.Equal(t, time.Date(2024, 3, 10, 14, 0, 0, 0, time.UTC), result)
	})
}
