package model

import (
	"time"

	"github.com/google/uuid"
)

// TimeStats summarizes time spent per Area (category) over [From, To), from two
// sources: categorized events (sessions at a real time, duration = end_time -
// start_time) and traces (minutes on a day, with no clock time).
type TimeStats struct {
	From  time.Time  `json:"from"`
	To    time.Time  `json:"to"`
	Areas []AreaStat `json:"areas"`
}

// AreaInfo describes the Area a stats entry is for. AreaID is nil for time without
// a category, which is grouped under a single "Uncategorized" entry. The Group*
// fields describe the Area's category group, if any, so clients can subtotal by group.
type AreaInfo struct {
	AreaID              *uuid.UUID `json:"area_id,omitempty"`
	AreaName            string     `json:"area_name"`
	AreaCode            string     `json:"area_code,omitempty"`
	AreaColor           string     `json:"area_color"`
	GroupID             *uuid.UUID `json:"group_id,omitempty"`
	GroupName           string     `json:"group_name,omitempty"`
	GroupColor          string     `json:"group_color,omitempty"`
	WeeklyTargetMinutes int        `json:"weekly_target_minutes"`
}

// AreaStat is the per-Area rollup.
type AreaStat struct {
	AreaInfo
	TotalMinutes  int               `json:"total_minutes"` // EventMinutes + TraceMinutes
	EventMinutes  int               `json:"event_minutes"`
	TraceMinutes  int               `json:"trace_minutes"`
	SubActivities []SubActivityStat `json:"sub_activities"`
}

// SubActivityStat breaks an Area's time down by event title (the "sub-activity",
// e.g. reading vs. listening within "French").
type SubActivityStat struct {
	Name    string `json:"name"`
	Minutes int    `json:"minutes"`
}

// WeeklyStats is time spent per Area per week over [From, To), counted as
// TimeStats counts it. Weeks holds the first day (in TZ) of every week the range
// touches, oldest first, empty ones included; each Area's Minutes lines up with it.
type WeeklyStats struct {
	From      time.Time    `json:"from"`
	To        time.Time    `json:"to"`
	TZ        string       `json:"tz"`
	WeekStart int          `json:"week_start"` // weekday weeks start on, Sun=0 … Sat=6
	Weeks     []Date       `json:"weeks"`
	Areas     []AreaWeekly `json:"areas"`
}

// AreaWeekly is an Area's total minutes (events + traces) per week of WeeklyStats.Weeks.
type AreaWeekly struct {
	AreaInfo
	Minutes []int `json:"minutes"`
}
