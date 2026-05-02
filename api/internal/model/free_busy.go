package model

import "time"

type TimeSlot struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

type FreeBusyEntry struct {
	Email  string     `json:"email"`
	Busy   []TimeSlot `json:"busy"`
	Status string     `json:"status,omitempty"` // "not_found" | "not_shared"
}
