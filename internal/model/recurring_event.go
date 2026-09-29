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

	// Exdates are occurrence start times excluded from the series (deleted, or edited
	// into standalone events). They still count toward MaxOccurrences.
	Exdates []time.Time `json:"exdates"`
	// SendInvitations is false for series imported from ICS, whose importer isn't the
	// organizer: their occurrences don't invite the attendees.
	SendInvitations bool `json:"-"`
	// Visibility ("public" | "private") is given to every occurrence.
	Visibility string `json:"visibility"`
	// WeekStart is the weekday (Sun=0 … Sat=6) a weekly series' weeks start on, which
	// decides which of its days go together when it repeats every 2+ weeks. nil means
	// Monday, as in RFC 5545.
	WeekStart *int `json:"week_start"`
}

type CreateRecurringEventRequest struct {
	CalendarID     *uuid.UUID `json:"calendar_id"`
	Title          string     `json:"title"       binding:"required"`
	Description    string     `json:"description"`
	Location       string     `json:"location"`
	StartTime      time.Time  `json:"start_time"  binding:"required"`
	EndTime        time.Time  `json:"end_time"    binding:"required"`
	Attendees      []string   `json:"attendees" binding:"omitempty,max=100,dive,email"`
	Reminders      []Reminder `json:"reminders"`
	Frequency      string     `json:"frequency"   binding:"required,oneof=daily weekly monthly yearly"`
	Interval       int        `json:"interval"`
	DaysOfWeek     []int      `json:"days_of_week"`
	EndDate        *time.Time `json:"end_date"`
	MaxOccurrences *int       `json:"max_occurrences" binding:"omitempty,min=1"`
	AllDay         bool       `json:"all_day"`
	Timezone       string     `json:"timezone"`
	CategoryID     *uuid.UUID `json:"category_id"`

	Exdates []time.Time `json:"exdates"`
	// NoInvitations: see RecurringEvent.SendInvitations. Set by ICS import only.
	NoInvitations bool   `json:"-"`
	Visibility    string `json:"visibility" binding:"omitempty,oneof=public private"`
	WeekStart     *int   `json:"week_start" binding:"omitempty,min=0,max=6"`
}

type UpdateRecurringEventRequest struct {
	CalendarID     *uuid.UUID `json:"calendar_id"`
	Title          *string    `json:"title"`
	Description    *string    `json:"description"`
	Location       *string    `json:"location"`
	StartTime      *time.Time `json:"start_time"`
	EndTime        *time.Time `json:"end_time"`
	Attendees      []string   `json:"attendees" binding:"omitempty,max=100,dive,email"`
	Reminders      []Reminder `json:"reminders"`
	Frequency      *string    `json:"frequency"  binding:"omitempty,oneof=daily weekly monthly yearly"`
	Interval       *int       `json:"interval"`
	DaysOfWeek     []int      `json:"days_of_week"`
	EndDate        *time.Time `json:"end_date"`
	MaxOccurrences *int       `json:"max_occurrences" binding:"omitempty,min=1"`
	AllDay         *bool      `json:"all_day"`
	Timezone       *string    `json:"timezone"`
	CategoryID     *uuid.UUID `json:"category_id"`
	Visibility     *string    `json:"visibility" binding:"omitempty,oneof=public private"`
	// WeekStart moves along with DaysOfWeek (UpdateAll); not settable through the API.
	WeekStart *int `json:"-"`
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
	Attendees   []string   `json:"attendees" binding:"omitempty,max=100,dive,email"`
	Reminders   []Reminder `json:"reminders"`
	AllDay      *bool      `json:"all_day"`
	Timezone    *string    `json:"timezone"`
	CategoryID  *uuid.UUID `json:"category_id"`
	Visibility  *string    `json:"visibility" binding:"omitempty,oneof=public private"`
}
