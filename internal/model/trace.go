package model

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Trace is a short stretch of time with no clock time: minutes spent on an Area
// on some day ("5 minutes of French here, 10 there"). Unlike a categorized event,
// which is a session at a real time, a trace never shows on the calendar grid —
// it only adds to the day's and the Area's totals.
type Trace struct {
	ID         uuid.UUID  `json:"id"`
	OwnerID    uuid.UUID  `json:"owner_id"`
	CategoryID *uuid.UUID `json:"category_id,omitempty"`
	Day        Date       `json:"day"`
	Minutes    int        `json:"minutes"`
	Note       string     `json:"note"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// MaxTraceMinutes caps one trace at a whole day.
const MaxTraceMinutes = 1440

type CreateTraceRequest struct {
	CategoryID *uuid.UUID `json:"category_id"`
	Day        Date       `json:"day" binding:"required"`
	Minutes    int        `json:"minutes" binding:"required,min=1,max=1440"`
	Note       string     `json:"note"`
}

type UpdateTraceRequest struct {
	// CategoryID is Optional so an explicit null clears it; an absent field keeps it.
	CategoryID Optional[uuid.UUID] `json:"category_id"`
	Day        *Date               `json:"day"`
	Minutes    *int                `json:"minutes"`
	Note       *string             `json:"note"`
}

// Date is a calendar day as "YYYY-MM-DD", with no time or zone to drift. It is
// stored in a DATE column (queries cast it with ::date and read it back as text).
type Date string

const dateLayout = "2006-01-02"

func (d *Date) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return errors.New("day must be a YYYY-MM-DD string")
	}
	if _, err := ParseDate(s); err != nil {
		return err
	}
	*d = Date(s)
	return nil
}

// ParseDate validates a "YYYY-MM-DD" day and returns it as midnight UTC.
func ParseDate(s string) (time.Time, error) {
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		return time.Time{}, errors.New("day must be YYYY-MM-DD")
	}
	if t.Year() < 1 { // Postgres has no year 0
		return time.Time{}, errors.New("day must be in year 0001 or later")
	}
	return t, nil
}
