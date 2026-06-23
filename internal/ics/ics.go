package ics

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	gics "github.com/arran4/golang-ical"
	"github.com/google/uuid"
	"github.com/hylin/calendar/internal/model"
)

var weekdayNames = [7]string{"SU", "MO", "TU", "WE", "TH", "FR", "SA"}

var weekdayByName = map[string]int{
	"SU": 0, "MO": 1, "TU": 2, "WE": 3, "TH": 4, "FR": 5, "SA": 6,
}

// Export builds an iCalendar document from a calendar's events and recurring rules.
// Expanded instances of recurring events are skipped; only the rule VEVENT is emitted.
func Export(calName string, events []model.Event, recurrings []model.RecurringEvent) string {
	cal := gics.NewCalendar()
	cal.SetProductId("-//Calendar App//EN")
	cal.SetVersion("2.0")
	cal.SetMethod(gics.MethodPublish)
	if calName != "" {
		cal.SetXWRCalName(calName)
	}

	for _, e := range events {
		if e.RecurringEventID != nil {
			continue
		}
		ve := cal.AddEvent(e.ID.String())
		ve.SetProperty(gics.ComponentPropertyDtstamp, e.CreatedAt.UTC().Format("20060102T150405Z"))
		ve.SetLastModifiedAt(e.UpdatedAt)
		if e.AllDay {
			ve.SetAllDayStartAt(e.StartTime)
			ve.SetAllDayEndAt(e.EndTime)
		} else {
			ve.SetStartAt(e.StartTime)
			ve.SetEndAt(e.EndTime)
		}
		ve.SetSummary(e.Title)
		if e.Description != "" {
			ve.SetDescription(e.Description)
		}
		if e.Location != "" {
			ve.SetLocation(e.Location)
		}
		for _, a := range e.Attendees {
			ve.AddAttendee(a)
		}
	}

	for _, rec := range recurrings {
		ve := cal.AddEvent(rec.ID.String())
		ve.SetProperty(gics.ComponentPropertyDtstamp, rec.CreatedAt.UTC().Format("20060102T150405Z"))
		ve.SetLastModifiedAt(rec.UpdatedAt)
		duration := time.Duration(rec.Duration)
		endTime := rec.StartTime.Add(duration)
		if rec.AllDay {
			ve.SetAllDayStartAt(rec.StartTime)
			ve.SetAllDayEndAt(endTime)
		} else {
			ve.SetStartAt(rec.StartTime)
			ve.SetEndAt(endTime)
		}
		ve.SetSummary(rec.Title)
		if rec.Description != "" {
			ve.SetDescription(rec.Description)
		}
		if rec.Location != "" {
			ve.SetLocation(rec.Location)
		}
		for _, a := range rec.Attendees {
			ve.AddAttendee(a)
		}
		ve.SetProperty(gics.ComponentPropertyRrule, buildRrule(rec))
	}

	return cal.Serialize()
}

func buildRrule(rec model.RecurringEvent) string {
	parts := []string{"FREQ=" + strings.ToUpper(rec.Frequency)}
	if rec.Interval > 1 {
		parts = append(parts, "INTERVAL="+strconv.Itoa(rec.Interval))
	}
	if len(rec.DaysOfWeek) > 0 {
		days := make([]string, 0, len(rec.DaysOfWeek))
		for _, d := range rec.DaysOfWeek {
			if d >= 0 && d < 7 {
				days = append(days, weekdayNames[d])
			}
		}
		if len(days) > 0 {
			parts = append(parts, "BYDAY="+strings.Join(days, ","))
		}
	}
	// COUNT takes precedence over UNTIL if both are set
	if rec.MaxOccurrences != nil {
		parts = append(parts, "COUNT="+strconv.Itoa(*rec.MaxOccurrences))
	} else if rec.EndDate != nil {
		parts = append(parts, "UNTIL="+rec.EndDate.UTC().Format("20060102T150405Z"))
	}
	return strings.Join(parts, ";")
}

// Import parses an iCalendar document and returns creation requests for one-off events
// and recurring event rules. The calendarID is injected into every request.
func Import(calendarID uuid.UUID, r io.Reader) ([]model.CreateEventRequest, []model.CreateRecurringEventRequest, error) {
	cal, err := gics.ParseCalendar(r)
	if err != nil {
		return nil, nil, fmt.Errorf("parse ics: %w", err)
	}

	var events []model.CreateEventRequest
	var recurrings []model.CreateRecurringEventRequest

	for _, ve := range cal.Events() {
		allDay, start, end := parseTimes(ve)
		if start.IsZero() {
			continue
		}
		if end.IsZero() || !end.After(start) {
			if allDay {
				end = start.AddDate(0, 0, 1)
			} else {
				end = start.Add(time.Hour)
			}
		}

		title := stringProp(ve, gics.ComponentPropertySummary)
		if title == "" {
			title = "Untitled"
		}
		description := stringProp(ve, gics.ComponentPropertyDescription)
		location := stringProp(ve, gics.ComponentPropertyLocation)

		var attendees []string
		for _, a := range ve.Attendees() {
			email := strings.TrimPrefix(a.Value, "MAILTO:")
			email = strings.TrimPrefix(email, "mailto:")
			if email != "" {
				attendees = append(attendees, email)
			}
		}

		tz := tzFromProp(ve, gics.ComponentPropertyDtStart)

		if ve.GetProperty(gics.ComponentPropertyRrule) != nil {
			rec, err := parseRrule(ve, calendarID, title, description, location, start, end, allDay, attendees, tz)
			if err == nil {
				recurrings = append(recurrings, rec)
			}
		} else {
			events = append(events, model.CreateEventRequest{
				CalendarID:  &calendarID,
				Title:       title,
				Description: description,
				Location:    location,
				StartTime:   start,
				EndTime:     end,
				Attendees:   attendees,
				AllDay:      allDay,
				Timezone:    tz,
			})
		}
	}

	return events, recurrings, nil
}

func stringProp(ve *gics.VEvent, prop gics.ComponentProperty) string {
	p := ve.GetProperty(prop)
	if p == nil {
		return ""
	}
	return p.Value
}

func tzFromProp(ve *gics.VEvent, prop gics.ComponentProperty) string {
	p := ve.GetProperty(prop)
	if p == nil {
		return "UTC"
	}
	if tzids := p.ICalParameters["TZID"]; len(tzids) > 0 && tzids[0] != "" {
		return tzids[0]
	}
	return "UTC"
}

func parseTimes(ve *gics.VEvent) (allDay bool, start, end time.Time) {
	sp := ve.GetProperty(gics.ComponentPropertyDtStart)
	ep := ve.GetProperty(gics.ComponentPropertyDtEnd)
	if sp == nil {
		return
	}
	val := sp.Value
	// DATE-only value → all-day
	if len(val) == 8 {
		allDay = true
		if t, err := time.Parse("20060102", val); err == nil {
			start = t.UTC()
		}
		if ep != nil {
			if t, err := time.Parse("20060102", ep.Value); err == nil {
				end = t.UTC()
			}
		}
		return
	}
	start = parseDateTime(sp)
	if ep != nil {
		end = parseDateTime(ep)
	}
	return
}

func parseDateTime(p *gics.IANAProperty) time.Time {
	val := p.Value
	// UTC: ends with Z
	if t, err := time.Parse("20060102T150405Z", val); err == nil {
		return t.UTC()
	}
	// Timezone-aware
	if tzids := p.ICalParameters["TZID"]; len(tzids) > 0 {
		if loc, err := time.LoadLocation(tzids[0]); err == nil {
			if t, err := time.ParseInLocation("20060102T150405", val, loc); err == nil {
				return t.UTC()
			}
		}
	}
	// Floating time — treat as UTC
	if t, err := time.Parse("20060102T150405", val); err == nil {
		return t.UTC()
	}
	return time.Time{}
}

func parseRrule(
	ve *gics.VEvent,
	calendarID uuid.UUID,
	title, description, location string,
	start, end time.Time,
	allDay bool,
	attendees []string,
	tz string,
) (model.CreateRecurringEventRequest, error) {
	prop := ve.GetProperty(gics.ComponentPropertyRrule)
	if prop == nil {
		return model.CreateRecurringEventRequest{}, fmt.Errorf("no RRULE")
	}

	params := map[string]string{}
	for _, part := range strings.Split(prop.Value, ";") {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) == 2 {
			params[strings.ToUpper(kv[0])] = kv[1]
		}
	}

	freq := strings.ToLower(params["FREQ"])
	switch freq {
	case "daily", "weekly", "monthly", "yearly":
	default:
		return model.CreateRecurringEventRequest{}, fmt.Errorf("unsupported FREQ: %s", freq)
	}

	interval := 1
	if v := params["INTERVAL"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			interval = n
		}
	}

	var daysOfWeek []int
	if byday := params["BYDAY"]; byday != "" {
		for _, d := range strings.Split(byday, ",") {
			d = strings.TrimSpace(d)
			// strip any ordinal prefix like "1MO", "-1FR"
			if len(d) > 2 {
				d = d[len(d)-2:]
			}
			if n, ok := weekdayByName[strings.ToUpper(d)]; ok {
				daysOfWeek = append(daysOfWeek, n)
			}
		}
	}

	var endDate *time.Time
	if until := params["UNTIL"]; until != "" {
		for _, layout := range []string{"20060102T150405Z", "20060102T150405", "20060102"} {
			if t, err := time.Parse(layout, until); err == nil {
				u := t.UTC()
				endDate = &u
				break
			}
		}
	}

	var maxOcc *int
	if count := params["COUNT"]; count != "" {
		if n, err := strconv.Atoi(count); err == nil && n > 0 {
			maxOcc = &n
		}
	}

	return model.CreateRecurringEventRequest{
		CalendarID:     &calendarID,
		Title:          title,
		Description:    description,
		Location:       location,
		StartTime:      start,
		EndTime:        end,
		Attendees:      attendees,
		Frequency:      freq,
		Interval:       interval,
		DaysOfWeek:     daysOfWeek,
		EndDate:        endDate,
		MaxOccurrences: maxOcc,
		AllDay:         allDay,
		Timezone:       tz,
	}, nil
}
