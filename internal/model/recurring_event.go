package model

import (
	"time"

	"github.com/google/uuid"
)

type RecurringEvent struct {
	ID             uuid.UUID  `json:"id"`
	OwnerID        uuid.UUID  `json:"owner_id"`
	CalendarID     uuid.UUID  `json:"calendar_id"`
	Title          string     `json:"title"`
	Description    string     `json:"description"`
	Location       string     `json:"location"`
	Duration       int64      `json:"duration_ns"`
	Attendees      []string   `json:"attendees"`
	Reminders      []Reminder `json:"reminders"`
	Frequency      string     `json:"frequency"`
	Interval       int        `json:"interval"`
	DaysOfWeek     []int      `json:"days_of_week"`
	EndDate        *time.Time `json:"end_date,omitempty"`
	MaxOccurrences *int       `json:"max_occurrences,omitempty"`
	AllDay         bool       `json:"all_day"`
	Timezone       string     `json:"timezone"`
	CategoryID     *uuid.UUID `json:"category_id,omitempty"`
	StartTime      time.Time  `json:"start_time"`
	GeneratedUntil time.Time  `json:"generated_until"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type CreateRecurringEventRequest struct {
	CalendarID     *uuid.UUID `json:"calendar_id"`
	Title          string     `json:"title"       binding:"required"`
	Description    string     `json:"description"`
	Location       string     `json:"location"`
	StartTime      time.Time  `json:"start_time"  binding:"required"`
	EndTime        time.Time  `json:"end_time"    binding:"required"`
	Attendees      []string   `json:"attendees"`
	Reminders      []Reminder `json:"reminders"`
	Frequency      string     `json:"frequency"   binding:"required,oneof=daily weekly monthly yearly"`
	Interval       int        `json:"interval"`
	DaysOfWeek     []int      `json:"days_of_week"`
	EndDate        *time.Time `json:"end_date"`
	MaxOccurrences *int       `json:"max_occurrences"`
	AllDay         bool       `json:"all_day"`
	Timezone       string     `json:"timezone"`
	CategoryID     *uuid.UUID `json:"category_id"`
}

type UpdateRecurringEventRequest struct {
	CalendarID     *uuid.UUID `json:"calendar_id"`
	Title          *string    `json:"title"`
	Description    *string    `json:"description"`
	Location       *string    `json:"location"`
	StartTime      *time.Time `json:"start_time"`
	EndTime        *time.Time `json:"end_time"`
	Attendees      []string   `json:"attendees"`
	Reminders      []Reminder `json:"reminders"`
	Frequency      *string    `json:"frequency"  binding:"omitempty,oneof=daily weekly monthly yearly"`
	Interval       *int       `json:"interval"`
	DaysOfWeek     []int      `json:"days_of_week"`
	EndDate        *time.Time `json:"end_date"`
	MaxOccurrences *int       `json:"max_occurrences"`
	AllDay         *bool      `json:"all_day"`
	Timezone       *string    `json:"timezone"`
	CategoryID     *uuid.UUID `json:"category_id"`
}

// UpdateRecurrenceRequest is used by PUT /events/:id/recurrence to edit one instance,
// this-and-following, or all instances of a recurring series.
type UpdateRecurrenceRequest struct {
	Scope       string     `json:"scope" binding:"required,oneof=this this_and_following all"`
	Title       *string    `json:"title"`
	Description *string    `json:"description"`
	Location    *string    `json:"location"`
	StartTime   *time.Time `json:"start_time"`
	EndTime     *time.Time `json:"end_time"`
	Attendees   []string   `json:"attendees"`
	Reminders   []Reminder `json:"reminders"`
	AllDay      *bool      `json:"all_day"`
	Timezone    *string    `json:"timezone"`
	CategoryID  *uuid.UUID `json:"category_id"`
	Visibility  *string    `json:"visibility"`
}
