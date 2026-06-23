package model

import (
	"time"

	"github.com/google/uuid"
)

// TimeStats summarizes time spent per Area (category) over [From, To), derived
// entirely from calendar events — a categorized event is a logged session, with
// duration = end_time - start_time. JSON field names are kept stable so the
// frontend stats panel renders the same shape regardless of the source table.
type TimeStats struct {
	From  time.Time  `json:"from"`
	To    time.Time  `json:"to"`
	Areas []AreaStat `json:"areas"`
}

// AreaStat is the per-Area rollup. AreaID is nil for events without a category,
// which are grouped under a single "Uncategorized" entry.
type AreaStat struct {
	AreaID              *uuid.UUID        `json:"area_id,omitempty"`
	AreaName            string            `json:"area_name"`
	AreaColor           string            `json:"area_color"`
	WeeklyTargetMinutes int               `json:"weekly_target_minutes"`
	TotalMinutes        int               `json:"total_minutes"`
	SubActivities       []SubActivityStat `json:"sub_activities"`
}

// SubActivityStat breaks an Area's time down by event title (the "sub-activity",
// e.g. reading vs. listening within "French").
type SubActivityStat struct {
	Name    string `json:"name"`
	Minutes int    `json:"minutes"`
}
