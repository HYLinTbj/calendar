package model

import (
	"time"

	"github.com/google/uuid"
)

// Reminder represents a single reminder for an event.
// Method is "email" or "notification" (both currently deliver via email).
type Reminder struct {
	Minutes int    `json:"minutes"`
	Method  string `json:"method"`
}

// AttendeeStatus pairs an attendee email with their RSVP response.
// Status values: "needs_action" | "accepted" | "declined" | "tentative"
type AttendeeStatus struct {
	Email  string `json:"email"`
	Status string `json:"status"`
}

type Event struct {
	ID               uuid.UUID  `json:"id"`
	OwnerID          uuid.UUID  `json:"owner_id"`
	CalendarID       uuid.UUID  `json:"calendar_id"`
	Title            string     `json:"title"`
	Description      string     `json:"description"`
	Location         string     `json:"location"`
	StartTime        time.Time  `json:"start_time"`
	EndTime          time.Time  `json:"end_time"`
	Attendees        []string         `json:"attendees"`
	AttendeeStatuses []AttendeeStatus `json:"attendee_statuses,omitempty"`
	Reminders        []Reminder       `json:"reminders"`
	AllDay           bool             `json:"all_day"`
	Timezone         string     `json:"timezone"`
	Visibility       string     `json:"visibility"` // "public" | "private"
	CategoryID       *uuid.UUID `json:"category_id,omitempty"`
	RecurringEventID *uuid.UUID `json:"recurring_event_id,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type CreateEventRequest struct {
	CalendarID  *uuid.UUID `json:"calendar_id"`
	Title       string     `json:"title"      binding:"required"`
	Description string     `json:"description"`
	Location    string     `json:"location"`
	StartTime   time.Time  `json:"start_time" binding:"required"`
	EndTime     time.Time  `json:"end_time"   binding:"required"`
	Attendees   []string   `json:"attendees"`
	Reminders   []Reminder `json:"reminders"`
	AllDay      bool       `json:"all_day"`
	Timezone    string     `json:"timezone"`
	CategoryID  *uuid.UUID `json:"category_id"`
	Visibility  string     `json:"visibility"`
}

type UpdateEventRequest struct {
	CalendarID  *uuid.UUID `json:"calendar_id"`
	Title       *string    `json:"title"`
	Description *string    `json:"description"`
	Location    *string    `json:"location"`
	StartTime   *time.Time `json:"start_time"`
	EndTime     *time.Time `json:"end_time"`
	Attendees   []string   `json:"attendees"`
	Reminders   []Reminder `json:"reminders"`
	AllDay      *bool      `json:"all_day"`
	Timezone    *string    `json:"timezone"`
	// Optional so an explicit null clears the category; absent keeps it.
	CategoryID Optional[uuid.UUID] `json:"category_id"`
	Visibility *string             `json:"visibility"`
}
