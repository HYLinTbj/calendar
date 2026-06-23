package repository

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/hylin/calendar/internal/model"
	"github.com/jackc/pgx/v5/pgxpool"
)

const windowDays = 60

type RecurringEventRepository struct {
	pool *pgxpool.Pool
}

func NewRecurringEventRepository(pool *pgxpool.Pool) *RecurringEventRepository {
	return &RecurringEventRepository{pool: pool}
}

const recurringCols = `id, owner_id, calendar_id, title, description, location, duration,
	attendees, reminders, frequency, interval, days_of_week,
	end_date, max_occurrences, all_day, timezone, category_id, start_time, generated_until, created_at, updated_at`

func scanRecurring(row interface{ Scan(...any) error }, r *model.RecurringEvent) error {
	var remindersRaw []byte
	err := row.Scan(
		&r.ID, &r.OwnerID, &r.CalendarID, &r.Title, &r.Description, &r.Location, &r.Duration,
		&r.Attendees, &remindersRaw, &r.Frequency, &r.Interval, &r.DaysOfWeek,
		&r.EndDate, &r.MaxOccurrences, &r.AllDay, &r.Timezone, &r.CategoryID, &r.StartTime, &r.GeneratedUntil, &r.CreatedAt, &r.UpdatedAt,
	)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(remindersRaw, &r.Reminders); err != nil {
		r.Reminders = []model.Reminder{}
	}
	return nil
}

func (r *RecurringEventRepository) Create(ctx context.Context, ownerID, calendarID uuid.UUID, req model.CreateRecurringEventRequest) (*model.RecurringEvent, error) {
	if req.Interval <= 0 {
		req.Interval = 1
	}
	if req.Attendees == nil {
		req.Attendees = []string{}
	}
	if req.DaysOfWeek == nil {
		req.DaysOfWeek = []int{}
	}
	durationNs := req.EndTime.Sub(req.StartTime).Nanoseconds()

	// generated_until starts one nanosecond before start_time so the first
	// occurrence is included when generateWindow runs.
	initialCursor := req.StartTime.Add(-1)
	horizon := time.Now().UTC().Add(windowDays * 24 * time.Hour)
	var rec model.RecurringEvent
	tz := req.Timezone
	if tz == "" {
		tz = "UTC"
	}
	remindersJSON := marshalReminders(req.Reminders)
	err := scanRecurring(r.pool.QueryRow(ctx, `
		INSERT INTO recurring_events
			(owner_id, calendar_id, title, description, location, duration,
			 attendees, reminders, frequency, interval, days_of_week,
			 end_date, max_occurrences, all_day, timezone, category_id, start_time, generated_until)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
		RETURNING `+recurringCols,
		ownerID, calendarID, req.Title, req.Description, req.Location,
		durationNs, req.Attendees, remindersJSON,
		req.Frequency, req.Interval, req.DaysOfWeek,
		req.EndDate, req.MaxOccurrences, req.AllDay, tz, req.CategoryID,
		req.StartTime, initialCursor,
	), &rec)
	if err != nil {
		return nil, err
	}

	if err := r.generateWindow(ctx, &rec, initialCursor, horizon); err != nil {
		return nil, err
	}
	return &rec, nil
}

func (r *RecurringEventRepository) GetByID(ctx context.Context, id, ownerID uuid.UUID) (*model.RecurringEvent, error) {
	var rec model.RecurringEvent
	err := scanRecurring(r.pool.QueryRow(ctx,
		`SELECT `+recurringCols+` FROM recurring_events WHERE id=$1 AND owner_id=$2`, id, ownerID,
	), &rec)
	return &rec, err
}

func (r *RecurringEventRepository) List(ctx context.Context, ownerID uuid.UUID) ([]model.RecurringEvent, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+recurringCols+` FROM recurring_events WHERE owner_id=$1 ORDER BY created_at ASC`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []model.RecurringEvent
	for rows.Next() {
		var rec model.RecurringEvent
		if err := scanRecurring(rows, &rec); err != nil {
			return nil, err
		}
		results = append(results, rec)
	}
	if results == nil {
		results = []model.RecurringEvent{}
	}
	return results, rows.Err()
}

func (r *RecurringEventRepository) ListByCalendar(ctx context.Context, ownerID, calendarID uuid.UUID) ([]model.RecurringEvent, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+recurringCols+` FROM recurring_events WHERE owner_id=$1 AND calendar_id=$2 ORDER BY start_time ASC`, ownerID, calendarID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []model.RecurringEvent
	for rows.Next() {
		var rec model.RecurringEvent
		if err := scanRecurring(rows, &rec); err != nil {
			return nil, err
		}
		results = append(results, rec)
	}
	if results == nil {
		results = []model.RecurringEvent{}
	}
	return results, rows.Err()
}

func (r *RecurringEventRepository) Update(ctx context.Context, id, ownerID uuid.UUID, req model.UpdateRecurringEventRequest) (*model.RecurringEvent, error) {
	rec, err := r.GetByID(ctx, id, ownerID)
	if err != nil {
		return nil, err
	}

	if req.CalendarID != nil {
		rec.CalendarID = *req.CalendarID
	}
	if req.Title != nil {
		rec.Title = *req.Title
	}
	if req.Description != nil {
		rec.Description = *req.Description
	}
	if req.Location != nil {
		rec.Location = *req.Location
	}
	if req.StartTime != nil {
		rec.StartTime = *req.StartTime
	}
	if req.EndTime != nil && req.StartTime != nil {
		rec.Duration = req.EndTime.Sub(*req.StartTime).Nanoseconds()
	} else if req.EndTime != nil {
		rec.Duration = req.EndTime.Sub(rec.StartTime).Nanoseconds()
	}
	if req.Attendees != nil {
		rec.Attendees = req.Attendees
	}
	if req.Reminders != nil {
		rec.Reminders = req.Reminders
	}
	if req.Frequency != nil {
		rec.Frequency = *req.Frequency
	}
	if req.Interval != nil && *req.Interval > 0 {
		rec.Interval = *req.Interval
	}
	if req.DaysOfWeek != nil {
		rec.DaysOfWeek = req.DaysOfWeek
	}
	// EndDate and MaxOccurrences: explicit nil means "clear", so always overwrite.
	rec.EndDate = req.EndDate
	rec.MaxOccurrences = req.MaxOccurrences
	if req.AllDay != nil {
		rec.AllDay = *req.AllDay
	}
	if req.Timezone != nil {
		rec.Timezone = *req.Timezone
	}
	if req.CategoryID != nil {
		rec.CategoryID = req.CategoryID
	}

	now := time.Now().UTC()
	horizon := now.Add(windowDays * 24 * time.Hour)

	// Delete all future generated events, then re-generate from now.
	_, err = r.pool.Exec(ctx,
		`DELETE FROM events WHERE recurring_event_id=$1 AND start_time > $2`, id, now)
	if err != nil {
		return nil, err
	}

	err = scanRecurring(r.pool.QueryRow(ctx, `
		UPDATE recurring_events
		SET calendar_id=$1, title=$2, description=$3, location=$4,
		    start_time=$5, duration=$6,
		    attendees=$7, reminders=$8, frequency=$9, interval=$10,
		    days_of_week=$11, end_date=$12, max_occurrences=$13, all_day=$14, timezone=$15, category_id=$16,
		    generated_until=$17, updated_at=NOW()
		WHERE id=$18 AND owner_id=$19
		RETURNING `+recurringCols,
		rec.CalendarID, rec.Title, rec.Description, rec.Location,
		rec.StartTime, rec.Duration,
		rec.Attendees, marshalReminders(rec.Reminders), rec.Frequency, rec.Interval,
		rec.DaysOfWeek, rec.EndDate, rec.MaxOccurrences, rec.AllDay, rec.Timezone, rec.CategoryID,
		now, id, ownerID,
	), rec)
	if err != nil {
		return nil, err
	}

	if err := r.generateWindow(ctx, rec, now, horizon); err != nil {
		return nil, err
	}
	rec.GeneratedUntil = horizon
	return rec, nil
}

// SplitAt truncates the series at pivot (exclusive) and creates a new series from pivot
// with the changes in req applied. Used for "this_and_following" scope edits.
func (r *RecurringEventRepository) SplitAt(ctx context.Context, recurringEventID, ownerID uuid.UUID, pivot time.Time, req model.UpdateRecurrenceRequest) (*model.RecurringEvent, error) {
	parent, err := r.GetByID(ctx, recurringEventID, ownerID)
	if err != nil {
		return nil, err
	}

	// Remove all instances from pivot onwards (they will be regenerated in the new series).
	if _, err = r.pool.Exec(ctx,
		`DELETE FROM events WHERE recurring_event_id = $1 AND start_time >= $2`,
		recurringEventID, pivot); err != nil {
		return nil, err
	}

	// Truncate the original series: set end_date to just before pivot so the scheduler
	// never generates instances at or after pivot for this series.
	cutoff := pivot.Add(-time.Nanosecond)
	if _, err = r.pool.Exec(ctx, `
		UPDATE recurring_events
		SET end_date = $1, max_occurrences = NULL, updated_at = NOW()
		WHERE id = $2`,
		cutoff, recurringEventID); err != nil {
		return nil, err
	}

	// Build the new series fields from the parent, applying req overrides.
	newTitle := parent.Title
	if req.Title != nil {
		newTitle = *req.Title
	}
	newDesc := parent.Description
	if req.Description != nil {
		newDesc = *req.Description
	}
	newLoc := parent.Location
	if req.Location != nil {
		newLoc = *req.Location
	}
	newAttendees := parent.Attendees
	if req.Attendees != nil {
		newAttendees = req.Attendees
	}
	newReminders := parent.Reminders
	if req.Reminders != nil {
		newReminders = req.Reminders
	}
	newAllDay := parent.AllDay
	if req.AllDay != nil {
		newAllDay = *req.AllDay
	}
	newTZ := parent.Timezone
	if req.Timezone != nil {
		newTZ = *req.Timezone
	}
	newCategory := parent.CategoryID
	if req.CategoryID != nil {
		newCategory = req.CategoryID
	}

	newStart := pivot
	if req.StartTime != nil {
		newStart = *req.StartTime
	}
	duration := time.Duration(parent.Duration)
	newEnd := newStart.Add(duration)
	if req.EndTime != nil {
		newEnd = *req.EndTime
	}

	createReq := model.CreateRecurringEventRequest{
		CalendarID:  &parent.CalendarID,
		Title:       newTitle,
		Description: newDesc,
		Location:    newLoc,
		StartTime:   newStart,
		EndTime:     newEnd,
		Attendees:   newAttendees,
		Reminders:   newReminders,
		Frequency:   parent.Frequency,
		Interval:        parent.Interval,
		DaysOfWeek:      parent.DaysOfWeek,
		EndDate:         parent.EndDate,
		AllDay:          newAllDay,
		Timezone:        newTZ,
		CategoryID:      newCategory,
	}

	return r.Create(ctx, parent.OwnerID, parent.CalendarID, createReq)
}

// UpdateAll updates every instance of a recurring series using the changes in req.
// If req.StartTime differs from instanceStartTime, the series anchor is shifted by the delta.
// Used for "all" scope edits.
func (r *RecurringEventRepository) UpdateAll(ctx context.Context, recurringEventID, ownerID uuid.UUID, instanceStartTime time.Time, req model.UpdateRecurrenceRequest) (*model.RecurringEvent, error) {
	updateReq := model.UpdateRecurringEventRequest{
		Title:       req.Title,
		Description: req.Description,
		Location:    req.Location,
		Attendees:   req.Attendees,
		Reminders:   req.Reminders,
		AllDay:      req.AllDay,
		Timezone:    req.Timezone,
		CategoryID:  req.CategoryID,
	}

	if req.StartTime != nil {
		parent, err := r.GetByID(ctx, recurringEventID, ownerID)
		if err != nil {
			return nil, err
		}
		delta := req.StartTime.Sub(instanceStartTime)
		shifted := parent.StartTime.Add(delta)
		updateReq.StartTime = &shifted
		if req.EndTime != nil {
			updateReq.EndTime = req.EndTime
		}
	} else if req.EndTime != nil {
		updateReq.EndTime = req.EndTime
	}

	return r.Update(ctx, recurringEventID, ownerID, updateReq)
}

func (r *RecurringEventRepository) Delete(ctx context.Context, id, ownerID uuid.UUID) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM recurring_events WHERE id=$1 AND owner_id=$2`, id, ownerID)
	return err
}

// GeneratePending is called by the scheduler service to extend windows for all rules.
func (r *RecurringEventRepository) GeneratePending(ctx context.Context) error {
	horizon := time.Now().UTC().Add(windowDays * 24 * time.Hour)
	rows, err := r.pool.Query(ctx,
		`SELECT `+recurringCols+` FROM recurring_events WHERE generated_until < $1`, horizon)
	if err != nil {
		return err
	}
	defer rows.Close()

	var rules []model.RecurringEvent
	for rows.Next() {
		var rec model.RecurringEvent
		if err := scanRecurring(rows, &rec); err != nil {
			return err
		}
		rules = append(rules, rec)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for i := range rules {
		rec := &rules[i]
		from := rec.GeneratedUntil
		if err := r.generateWindow(ctx, rec, from, horizon); err != nil {
			return err
		}
	}
	return nil
}

func (r *RecurringEventRepository) generateWindow(ctx context.Context, rec *model.RecurringEvent, from, until time.Time) error {
	occurrences := nextOccurrences(rec, from, until)
	if len(occurrences) == 0 {
		return nil
	}

	duration := time.Duration(rec.Duration)

	remindersJSON := marshalReminders(rec.Reminders)
	for _, start := range occurrences {
		end := start.Add(duration)
		_, err := r.pool.Exec(ctx, `
			INSERT INTO events
				(owner_id, calendar_id, title, description, location,
				 start_time, end_time, attendees, reminders, all_day, timezone, category_id, recurring_event_id)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
			ON CONFLICT (recurring_event_id, start_time) WHERE recurring_event_id IS NOT NULL DO NOTHING`,
			rec.OwnerID, rec.CalendarID, rec.Title, rec.Description, rec.Location,
			start, end, rec.Attendees, remindersJSON, rec.AllDay, rec.Timezone, rec.CategoryID, rec.ID,
		)
		if err != nil {
			return err
		}
	}

	if len(rec.Attendees) > 0 {
		// Bulk-insert invitations for all events in the generated window.
		// ON CONFLICT keeps existing rows so statuses are preserved on re-runs.
		_, err := r.pool.Exec(ctx, `
			INSERT INTO event_invitations (event_id, email)
			SELECT e.id, unnest($2::text[])
			FROM events e
			WHERE e.recurring_event_id = $1
			  AND e.start_time > $3
			  AND e.start_time <= $4
			ON CONFLICT (event_id, email) DO NOTHING`,
			rec.ID, rec.Attendees, from, until)
		if err != nil {
			return err
		}
	}

	_, err := r.pool.Exec(ctx,
		`UPDATE recurring_events SET generated_until=$1 WHERE id=$2`, until, rec.ID)
	return err
}

// nextOccurrences returns all occurrence start times in (from, until].
// Anchors from StartTime to preserve schedule alignment after updates.
// Steps dates in the event's timezone so wall-clock time is preserved across DST changes.
func nextOccurrences(rec *model.RecurringEvent, from, until time.Time) []time.Time {
	loc, err := time.LoadLocation(rec.Timezone)
	if err != nil {
		loc = time.UTC
	}

	// Wall-clock components of the anchor, used to reconstruct each occurrence.
	anchor := rec.StartTime.In(loc)
	h, m, s, ns := anchor.Hour(), anchor.Minute(), anchor.Second(), anchor.Nanosecond()

	var results []time.Time
	current := rec.StartTime
	for !current.After(from) {
		current = step(rec, current, loc, h, m, s, ns)
	}
	for !current.After(until) {
		if rec.EndDate != nil && current.After(*rec.EndDate) {
			break
		}
		results = append(results, current)
		current = step(rec, current, loc, h, m, s, ns)
	}
	return results
}

// step advances t by one recurrence interval, reconstructing the wall-clock time
// in loc so that DST transitions don't shift the displayed hour.
func step(rec *model.RecurringEvent, t time.Time, loc *time.Location, h, m, s, ns int) time.Time {
	local := t.In(loc)
	n := rec.Interval
	rebuild := func(d time.Time) time.Time {
		return time.Date(d.Year(), d.Month(), d.Day(), h, m, s, ns, loc).UTC()
	}
	switch rec.Frequency {
	case "daily":
		return rebuild(local.AddDate(0, 0, n))
	case "weekly":
		if len(rec.DaysOfWeek) == 0 {
			return rebuild(local.AddDate(0, 0, 7*n))
		}
		candidate := local.AddDate(0, 0, 1)
		limit := local.AddDate(0, 0, 7*n)
		for !candidate.After(limit) {
			for _, d := range rec.DaysOfWeek {
				if int(candidate.Weekday()) == d {
					return rebuild(candidate)
				}
			}
			candidate = candidate.AddDate(0, 0, 1)
		}
		return rebuild(limit)
	case "monthly":
		return rebuild(local.AddDate(0, n, 0))
	case "yearly":
		return rebuild(local.AddDate(n, 0, 0))
	default:
		return rebuild(local.AddDate(0, 0, 1))
	}
}

