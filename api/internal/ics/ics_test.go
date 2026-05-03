package ics_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hylin/calendar/api/internal/ics"
	"github.com/hylin/calendar/api/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	fixedID    = uuid.MustParse("00000000-0000-0000-0000-000000000001")
	fixedCalID = uuid.MustParse("00000000-0000-0000-0000-000000000002")
	fixedTime  = time.Date(2024, 6, 15, 9, 0, 0, 0, time.UTC)
	fixedEnd   = time.Date(2024, 6, 15, 10, 0, 0, 0, time.UTC)
)

func intPtr(n int) *int { return &n }

// --- Export tests ---

func TestExport_SingleEvent(t *testing.T) {
	e := model.Event{
		ID:          fixedID,
		Title:       "Team Meeting",
		Description: "Quarterly review",
		Location:    "Conference Room 1",
		StartTime:   fixedTime,
		EndTime:     fixedEnd,
		Attendees:   []string{"alice@example.com", "bob@example.com"},
		CreatedAt:   fixedTime,
		UpdatedAt:   fixedTime,
	}
	out := ics.Export("Work Calendar", []model.Event{e}, nil)

	assert.Contains(t, out, "BEGIN:VCALENDAR")
	assert.Contains(t, out, "BEGIN:VEVENT")
	assert.Contains(t, out, "SUMMARY:Team Meeting")
	assert.Contains(t, out, "DESCRIPTION:Quarterly review")
	assert.Contains(t, out, "LOCATION:Conference Room 1")
	assert.Contains(t, out, "ATTENDEE")
	assert.Contains(t, out, "alice@example.com")
	assert.Contains(t, out, "END:VEVENT")
	assert.Contains(t, out, "END:VCALENDAR")
}

func TestExport_AllDayEvent(t *testing.T) {
	start := time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 3, 16, 0, 0, 0, 0, time.UTC)
	e := model.Event{
		ID:        fixedID,
		Title:     "All Day Event",
		StartTime: start,
		EndTime:   end,
		AllDay:    true,
		CreatedAt: start,
		UpdatedAt: start,
	}
	out := ics.Export("", []model.Event{e}, nil)

	// All-day events use DATE format (no time component)
	assert.Contains(t, out, "20240315")
	assert.NotContains(t, out, "20240315T")
}

func TestExport_RecurringEventEmitsRrule(t *testing.T) {
	endDate := time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC)
	rec := model.RecurringEvent{
		ID:         fixedID,
		Title:      "Weekly Standup",
		Frequency:  "weekly",
		Interval:   2,
		DaysOfWeek: []int{1, 3}, // Mon, Wed
		EndDate:    &endDate,
		StartTime:  fixedTime,
		Duration:   int64(time.Hour),
		CreatedAt:  fixedTime,
		UpdatedAt:  fixedTime,
	}
	out := ics.Export("", nil, []model.RecurringEvent{rec})

	assert.Contains(t, out, "RRULE:")
	assert.Contains(t, out, "FREQ=WEEKLY")
	assert.Contains(t, out, "INTERVAL=2")
	assert.Contains(t, out, "BYDAY=MO,WE")
	assert.Contains(t, out, "UNTIL=")
}

func TestExport_ExpandedInstanceSkipped(t *testing.T) {
	recurID := uuid.MustParse("00000000-0000-0000-0000-000000000099")
	e := model.Event{
		ID:               fixedID,
		Title:            "Instance",
		StartTime:        fixedTime,
		EndTime:          fixedEnd,
		RecurringEventID: &recurID, // marks this as an expanded instance
		CreatedAt:        fixedTime,
		UpdatedAt:        fixedTime,
	}
	out := ics.Export("", []model.Event{e}, nil)

	// The instance should be skipped; only the skeleton calendar should be emitted.
	assert.NotContains(t, out, "BEGIN:VEVENT")
}

func TestExport_EmptyInputs(t *testing.T) {
	out := ics.Export("Empty", nil, nil)

	assert.Contains(t, out, "BEGIN:VCALENDAR")
	assert.Contains(t, out, "END:VCALENDAR")
	assert.NotContains(t, out, "BEGIN:VEVENT")
}

func TestExport_buildRrule_Minimal(t *testing.T) {
	rec := model.RecurringEvent{
		ID:        fixedID,
		Title:     "Daily",
		Frequency: "daily",
		Interval:  1,
		StartTime: fixedTime,
		Duration:  int64(time.Hour),
		CreatedAt: fixedTime,
		UpdatedAt: fixedTime,
	}
	out := ics.Export("", nil, []model.RecurringEvent{rec})

	assert.Contains(t, out, "FREQ=DAILY")
	// interval=1 should not add INTERVAL= to keep output minimal
	assert.NotContains(t, out, "INTERVAL=")
}

func TestExport_buildRrule_CountTakesPrecedenceOverUntil(t *testing.T) {
	endDate := time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC)
	maxOcc := 5
	rec := model.RecurringEvent{
		ID:             fixedID,
		Title:          "Limited",
		Frequency:      "daily",
		Interval:       1,
		EndDate:        &endDate,
		MaxOccurrences: &maxOcc,
		StartTime:      fixedTime,
		Duration:       int64(time.Hour),
		CreatedAt:      fixedTime,
		UpdatedAt:      fixedTime,
	}
	out := ics.Export("", nil, []model.RecurringEvent{rec})

	assert.Contains(t, out, "COUNT=5")
	assert.NotContains(t, out, "UNTIL=")
}

// --- Import tests ---

func TestImport_OneOffEvent(t *testing.T) {
	icsData := `BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
UID:test-uid-001
DTSTART:20240615T090000Z
DTEND:20240615T100000Z
SUMMARY:Team Meeting
DESCRIPTION:Quarterly review
LOCATION:Room 1
ATTENDEE:MAILTO:alice@example.com
END:VEVENT
END:VCALENDAR`

	events, recurrings, err := ics.Import(fixedCalID, strings.NewReader(icsData))
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Empty(t, recurrings)

	ev := events[0]
	assert.Equal(t, "Team Meeting", ev.Title)
	assert.Equal(t, "Quarterly review", ev.Description)
	assert.Equal(t, "Room 1", ev.Location)
	assert.Equal(t, fixedTime, ev.StartTime)
	assert.Equal(t, fixedEnd, ev.EndTime)
	assert.Contains(t, ev.Attendees, "alice@example.com")
	assert.Equal(t, &fixedCalID, ev.CalendarID)
}

func TestImport_AllDayEvent(t *testing.T) {
	icsData := `BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
UID:allday-001
DTSTART;VALUE=DATE:20240615
DTEND;VALUE=DATE:20240616
SUMMARY:All Day
END:VEVENT
END:VCALENDAR`

	events, _, err := ics.Import(fixedCalID, strings.NewReader(icsData))
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.True(t, events[0].AllDay)
}

func TestImport_RecurringDaily(t *testing.T) {
	icsData := `BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
UID:rec-001
DTSTART:20240101T100000Z
DTEND:20240101T110000Z
RRULE:FREQ=DAILY;COUNT=5
SUMMARY:Daily Standup
END:VEVENT
END:VCALENDAR`

	events, recurrings, err := ics.Import(fixedCalID, strings.NewReader(icsData))
	require.NoError(t, err)
	assert.Empty(t, events)
	require.Len(t, recurrings, 1)

	rec := recurrings[0]
	assert.Equal(t, "daily", rec.Frequency)
	require.NotNil(t, rec.MaxOccurrences)
	assert.Equal(t, 5, *rec.MaxOccurrences)
	assert.Nil(t, rec.EndDate)
}

func TestImport_RecurringWeeklyWithByday(t *testing.T) {
	icsData := `BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
UID:rec-002
DTSTART:20240101T100000Z
DTEND:20240101T110000Z
RRULE:FREQ=WEEKLY;INTERVAL=2;BYDAY=MO,WE
SUMMARY:Bi-weekly
END:VEVENT
END:VCALENDAR`

	_, recurrings, err := ics.Import(fixedCalID, strings.NewReader(icsData))
	require.NoError(t, err)
	require.Len(t, recurrings, 1)

	rec := recurrings[0]
	assert.Equal(t, "weekly", rec.Frequency)
	assert.Equal(t, 2, rec.Interval)
	assert.Equal(t, []int{1, 3}, rec.DaysOfWeek) // MO=1, WE=3
}

func TestImport_BydayOrdinalPrefixStripped(t *testing.T) {
	icsData := `BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
UID:rec-003
DTSTART:20240101T100000Z
DTEND:20240101T110000Z
RRULE:FREQ=MONTHLY;BYDAY=1MO,-1FR
SUMMARY:Monthly
END:VEVENT
END:VCALENDAR`

	_, recurrings, err := ics.Import(fixedCalID, strings.NewReader(icsData))
	require.NoError(t, err)
	require.Len(t, recurrings, 1)
	// 1MO → MO=1, -1FR → FR=5
	assert.Equal(t, []int{1, 5}, recurrings[0].DaysOfWeek)
}

func TestImport_MissingDtstart_EventSkipped(t *testing.T) {
	icsData := `BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
UID:nodtstart-001
SUMMARY:No Start Time
END:VEVENT
END:VCALENDAR`

	events, recurrings, err := ics.Import(fixedCalID, strings.NewReader(icsData))
	require.NoError(t, err)
	assert.Empty(t, events)
	assert.Empty(t, recurrings)
}

func TestImport_InvalidICS_ReturnsError(t *testing.T) {
	_, _, err := ics.Import(fixedCalID, strings.NewReader("not valid ics content"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parse ics")
}

func TestImport_EndBeforeStart_DefaultsEndTime(t *testing.T) {
	icsData := `BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
UID:badend-001
DTSTART:20240615T090000Z
DTEND:20240615T080000Z
SUMMARY:Bad End
END:VEVENT
END:VCALENDAR`

	events, _, err := ics.Import(fixedCalID, strings.NewReader(icsData))
	require.NoError(t, err)
	require.Len(t, events, 1)
	// End before start → defaulted to start + 1 hour
	assert.Equal(t, fixedTime.Add(time.Hour), events[0].EndTime)
}
