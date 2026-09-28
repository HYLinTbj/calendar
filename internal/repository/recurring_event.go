package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/hylin/calendar/internal/model"
	"github.com/jackc/pgx/v5/pgxpool"
)

const windowDays = 60

// maxExtendAhead bounds how far ahead ExtendThrough materializes occurrences on demand.
const maxExtendAhead = 3 * 365 * 24 * time.Hour

type RecurringEventRepository struct {
	pool *pgxpool.Pool
}

func NewRecurringEventRepository(pool *pgxpool.Pool) *RecurringEventRepository {
	return &RecurringEventRepository{pool: pool}
}

const recurringCols = `id, owner_id, calendar_id, title, description, location, duration,
	attendees, reminders, frequency, interval, days_of_week,
	end_date, max_occurrences, all_day, timezone, category_id, start_time, generated_until, created_at, updated_at, exdates,
	send_invitations, visibility`

func scanRecurring(row interface{ Scan(...any) error }, r *model.RecurringEvent) error {
	var remindersRaw []byte
	err := row.Scan(
		&r.ID, &r.OwnerID, &r.CalendarID, &r.Title, &r.Description, &r.Location, &r.Duration,
		&r.Attendees, &remindersRaw, &r.Frequency, &r.Interval, &r.DaysOfWeek,
		&r.EndDate, &r.MaxOccurrences, &r.AllDay, &r.Timezone, &r.CategoryID, &r.StartTime, &r.GeneratedUntil, &r.CreatedAt, &r.UpdatedAt,
		&r.Exdates, &r.SendInvitations, &r.Visibility,
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
	if req.Exdates == nil {
		req.Exdates = []time.Time{}
	}
	if req.Visibility == "" {
		req.Visibility = "public"
	}
	durationNs := req.EndTime.Sub(req.StartTime).Nanoseconds()

	// generated_until starts just before start_time so the first occurrence is
	// included when generateWindow runs. Margin must exceed Postgres's timestamptz
	// precision (microseconds): rec.StartTime below is read back from the DB via
	// RETURNING, not the req.StartTime used to compute this cursor, and a 1ns margin
	// could be erased by that round-trip's truncation, causing the fast-forward loop
	// in nextOccurrences to wrongly treat occurrence #1 as already generated.
	initialCursor := req.StartTime.Add(-time.Microsecond)
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
			 end_date, max_occurrences, all_day, timezone, category_id, start_time, generated_until, exdates,
			 send_invitations, visibility)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)
		RETURNING `+recurringCols,
		ownerID, calendarID, req.Title, req.Description, req.Location,
		durationNs, req.Attendees, remindersJSON,
		req.Frequency, req.Interval, req.DaysOfWeek,
		req.EndDate, req.MaxOccurrences, req.AllDay, tz, req.CategoryID,
		req.StartTime, initialCursor, req.Exdates,
		!req.NoInvitations, req.Visibility,
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
	old := *rec

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
	if req.Visibility != nil {
		rec.Visibility = *req.Visibility
	}
	// Keep exclusions on the occurrences they belong to. With the same pattern, an edit
	// that moves the anchor (or toggles all-day, or changes zone) moves every occurrence,
	// so carry each exclusion to the occurrence at the same position. A new pattern has
	// no such correspondence; keep the times, which still exclude any that recur.
	remap := slices.Equal(old.DaysOfWeek, rec.DaysOfWeek) && old.Frequency == rec.Frequency && old.Interval == rec.Interval
	if remap {
		rec.Exdates = remapExdates(&old, rec, 0)
	} else if rec.Exdates == nil {
		rec.Exdates = []time.Time{} // NOT NULL column
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
		    generated_until=$17, exdates=$20, visibility=$21, updated_at=NOW()
		WHERE id=$18 AND owner_id=$19
		RETURNING `+recurringCols,
		rec.CalendarID, rec.Title, rec.Description, rec.Location,
		rec.StartTime, rec.Duration,
		rec.Attendees, marshalReminders(rec.Reminders), rec.Frequency, rec.Interval,
		rec.DaysOfWeek, rec.EndDate, rec.MaxOccurrences, rec.AllDay, rec.Timezone, rec.CategoryID,
		now, id, ownerID, rec.Exdates, rec.Visibility,
	), rec)
	if err != nil {
		return nil, err
	}
	// Future occurrences are regenerated below; past ones keep their details, but not
	// their privacy — hiding a series should hide its history too.
	if old.Visibility != rec.Visibility {
		if _, err := r.pool.Exec(ctx,
			`UPDATE events SET visibility = $1 WHERE recurring_event_id = $2`, rec.Visibility, id); err != nil {
			return nil, err
		}
		// Occurrences edited on their own too — but only to hide them: making a series
		// public shouldn't expose exceptions made private on purpose.
		if rec.Visibility == "private" {
			if _, err := r.pool.Exec(ctx,
				`UPDATE events SET visibility = 'private' WHERE detached_from = $1`, id); err != nil {
				return nil, err
			}
		}
	}
	if remap {
		if err := r.moveExceptions(ctx, &old, rec, 0); err != nil {
			return nil, err
		}
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
	newVisibility := parent.Visibility
	if req.Visibility != nil {
		newVisibility = *req.Visibility
	}

	newStart := pivot
	if req.StartTime != nil {
		newStart = *req.StartTime
	}
	// The new series' occurrence #1 is the parent's first at pivot. Exclusions from
	// there on carry over to the matching occurrences of the new series, so ones deleted
	// or edited on their own before the split don't come back.
	before := occurrencesBefore(parent, pivot)
	forkRule := model.RecurringEvent{
		StartTime: newStart, Frequency: parent.Frequency, Interval: parent.Interval,
		DaysOfWeek: parent.DaysOfWeek, AllDay: newAllDay, Timezone: newTZ,
	}
	duration := time.Duration(parent.Duration)
	newEnd := newStart.Add(duration)
	if req.EndTime != nil {
		newEnd = *req.EndTime
	}

	createReq := model.CreateRecurringEventRequest{
		CalendarID:     &parent.CalendarID,
		Title:          newTitle,
		Description:    newDesc,
		Location:       newLoc,
		StartTime:      newStart,
		EndTime:        newEnd,
		Attendees:      newAttendees,
		Reminders:      newReminders,
		Frequency:      parent.Frequency,
		Interval:       parent.Interval,
		DaysOfWeek:     parent.DaysOfWeek,
		EndDate:        parent.EndDate,
		MaxOccurrences: remainingOccurrences(parent, pivot),
		AllDay:         newAllDay,
		Timezone:       newTZ,
		CategoryID:     newCategory,
		Exdates:        remapExdates(parent, &forkRule, before),
		NoInvitations:  !parent.SendInvitations,
		Visibility:     newVisibility,
	}

	// The parent's instances from pivot on are regenerated by the new series.
	if _, err := r.pool.Exec(ctx,
		`DELETE FROM events WHERE recurring_event_id = $1 AND start_time >= $2`,
		parent.ID, pivot); err != nil {
		return nil, err
	}
	fork, err := r.Create(ctx, parent.OwnerID, parent.CalendarID, createReq)
	if err != nil {
		return nil, err
	}
	// Exceptions from pivot on belong to the new series now, and are hidden with it
	// (see Update).
	if err := r.moveExceptions(ctx, parent, fork, before); err != nil {
		return nil, err
	}
	if fork.Visibility == "private" {
		if _, err := r.pool.Exec(ctx,
			`UPDATE events SET visibility = 'private' WHERE detached_from = $1`, fork.ID); err != nil {
			return nil, err
		}
	}
	if err := r.endSeries(ctx, parent, pivot); err != nil {
		return nil, err
	}
	return fork, nil
}

// TruncateAt ends a series just before pivot: instances from pivot on are deleted and
// no more are generated, while earlier ones (possibly already-logged time) are kept.
// Used for "this and following" deletes.
func (r *RecurringEventRepository) TruncateAt(ctx context.Context, recurringEventID, ownerID uuid.UUID, pivot time.Time) error {
	parent, err := r.GetByID(ctx, recurringEventID, ownerID)
	if err != nil {
		return err
	}
	// Includes occurrences edited on their own, matched by the occurrence they were.
	if _, err := r.pool.Exec(ctx, `
		DELETE FROM events
		WHERE (recurring_event_id = $1 AND start_time >= $2)
		   OR (detached_from = $1 AND original_start >= $2)`,
		parent.ID, pivot); err != nil {
		return err
	}
	return r.endSeries(ctx, parent, pivot)
}

// ExtendThrough materializes occurrences up to until (capped at maxExtendAhead from now)
// for the series whose calendars userID can see, optionally only calendarID's. The
// scheduler keeps a rolling windowDays of occurrences; this lets a view that looks
// further ahead show the series too.
func (r *RecurringEventRepository) ExtendThrough(ctx context.Context, userID uuid.UUID, calendarID *uuid.UUID, until time.Time) error {
	if limit := time.Now().UTC().Add(maxExtendAhead); until.After(limit) {
		until = limit
	}
	query := `SELECT ` + recurringCols + ` FROM recurring_events
		WHERE generated_until < $1
		  AND (end_date IS NULL OR end_date > generated_until)
		  AND (owner_id = $2 OR calendar_id IN (SELECT calendar_id FROM calendar_shares WHERE shared_with_user_id = $2))`
	args := []any{until, userID}
	if calendarID != nil {
		query += ` AND calendar_id = $3`
		args = append(args, *calendarID)
	}
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return err
	}
	var recs []model.RecurringEvent
	for rows.Next() {
		var rec model.RecurringEvent
		if err := scanRecurring(rows, &rec); err != nil {
			rows.Close()
			return err
		}
		recs = append(recs, rec)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range recs {
		if err := r.generateWindow(ctx, &recs[i], recs[i].GeneratedUntil, until); err != nil {
			return fmt.Errorf("recurring rule %s: %w", recs[i].ID, err)
		}
	}
	return nil
}

// RepairLegacyExceptions brings instances that were edited on their own before
// exceptions were tracked in line with how such edits work now. Back then an edit (a
// drag, a Log view change) left the instance linked to its series, possibly moved off
// its occurrence time, so a later series-wide edit deleted it and regenerated the
// original occurrence. Each one is detached as an exception of its series, with its
// occurrence excluded. They're told apart by updated_at > created_at: nothing updates
// a generated instance in place any more (edits detach it). Runs at startup; once the
// data is repaired it finds nothing. Returns how many instances it repaired.
func (r *RecurringEventRepository) RepairLegacyExceptions(ctx context.Context) (int, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT DISTINCT recurring_event_id FROM events
		WHERE recurring_event_id IS NOT NULL AND updated_at > created_at`)
	if err != nil {
		return 0, err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	total := 0
	for _, id := range ids {
		n, err := r.repairSeries(ctx, id)
		if err != nil {
			return total, fmt.Errorf("series %s: %w", id, err)
		}
		total += n
	}
	return total, nil
}

// repairSeries detaches series id's edited instances (see RepairLegacyExceptions). An
// instance still at one of the series' occurrence times came from that occurrence. A
// moved one's occurrence wasn't recorded, so it's taken to be the nearest generated
// occurrence left without an instance (a drag moves an occurrence away from its own
// slot); with none free, it's detached without excluding anything.
func (r *RecurringEventRepository) repairSeries(ctx context.Context, id uuid.UUID) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var rec model.RecurringEvent
	if err := scanRecurring(tx.QueryRow(ctx,
		`SELECT `+recurringCols+` FROM recurring_events WHERE id = $1 FOR UPDATE`, id), &rec); err != nil {
		return 0, err
	}
	type instance struct {
		id     uuid.UUID
		start  time.Time
		edited bool
	}
	rows, err := tx.Query(ctx,
		`SELECT id, start_time, updated_at > created_at FROM events WHERE recurring_event_id = $1 ORDER BY start_time`, id)
	if err != nil {
		return 0, err
	}
	var insts []instance
	for rows.Next() {
		var in instance
		if err := rows.Scan(&in.id, &in.start, &in.edited); err != nil {
			rows.Close()
			return 0, err
		}
		insts = append(insts, in)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	// The occurrences generated so far, and which of them still have an instance.
	slots := nextOccurrences(&rec, rec.StartTime.Add(-time.Microsecond), rec.GeneratedUntil)
	isSlot := make(map[int64]bool, len(slots))
	for _, t := range slots {
		isSlot[t.UnixMicro()] = true
	}
	occupied := map[int64]bool{}
	for _, in := range insts {
		if isSlot[in.start.UnixMicro()] {
			occupied[in.start.UnixMicro()] = true
		}
	}

	excluded := map[int64]bool{}
	for _, t := range rec.Exdates {
		excluded[t.UnixMicro()] = true
	}
	exdates := rec.Exdates
	if exdates == nil {
		exdates = []time.Time{}
	}
	repaired := 0
	for _, in := range insts {
		if !in.edited {
			continue
		}
		original, found := in.start, isSlot[in.start.UnixMicro()]
		if !found {
			var best time.Time
			for _, t := range slots {
				if occupied[t.UnixMicro()] {
					continue
				}
				if !found || t.Sub(in.start).Abs() < best.Sub(in.start).Abs() {
					best, found = t, true
				}
			}
			if found {
				original = best
				occupied[best.UnixMicro()] = true
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE events SET recurring_event_id = NULL, detached_from = $1, original_start = $2
			WHERE id = $3`, id, original, in.id); err != nil {
			return 0, err
		}
		if found && !excluded[original.UnixMicro()] {
			exdates = append(exdates, original)
			excluded[original.UnixMicro()] = true
		}
		repaired++
	}
	if _, err := tx.Exec(ctx,
		`UPDATE recurring_events SET exdates = $1 WHERE id = $2`, exdates, id); err != nil {
		return 0, err
	}
	return repaired, tx.Commit(ctx)
}

// endSeries stops rec generating from pivot on, once its instances from pivot on have
// been deleted or handed to another series. If nothing of it is left (no instance,
// linked or edited) the rule is deleted rather than kept empty. That's decided from the
// rows, not the anchor: a series-wide edit that moved the anchor later keeps the
// earlier instances, and deleting the rule would cascade to them.
func (r *RecurringEventRepository) endSeries(ctx context.Context, rec *model.RecurringEvent, pivot time.Time) error {
	var left bool
	if err := r.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM events WHERE recurring_event_id = $1 OR detached_from = $1)`,
		rec.ID).Scan(&left); err != nil {
		return err
	}
	if !left {
		return r.Delete(ctx, rec.ID, rec.OwnerID)
	}
	// end_date just before pivot stops generation; what's left already fits within any
	// cap, so it's cleared.
	_, err := r.pool.Exec(ctx, `
		UPDATE recurring_events
		SET end_date = $1, max_occurrences = NULL, updated_at = NOW()
		WHERE id = $2`,
		pivot.Add(-time.Nanosecond), rec.ID)
	return err
}

// moveExceptions hands from's edited-on-their-own instances over to series to (which
// may be from itself, updated), re-pointing each at the matching occurrence of to (see
// remapOccurrences). With skip > 0 only those from from's occurrence skip+1 on move —
// the ones a series forked there takes over.
func (r *RecurringEventRepository) moveExceptions(ctx context.Context, from, to *model.RecurringEvent, skip int) error {
	rows, err := r.pool.Query(ctx,
		`SELECT id, original_start FROM events WHERE detached_from = $1 AND original_start IS NOT NULL`, from.ID)
	if err != nil {
		return err
	}
	var ids []uuid.UUID
	var starts []time.Time
	for rows.Next() {
		var id uuid.UUID
		var t time.Time
		if err := rows.Scan(&id, &t); err != nil {
			rows.Close()
			return err
		}
		ids, starts = append(ids, id), append(starts, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	moved := remapOccurrences(from, to, skip, starts)
	for i, id := range ids {
		t, ok := moved[starts[i].UnixMicro()]
		if !ok {
			continue
		}
		if _, err := r.pool.Exec(ctx,
			`UPDATE events SET detached_from = $1, original_start = $2 WHERE id = $3`, to.ID, t, id); err != nil {
			return err
		}
	}
	return nil
}

// UpdateAll updates every instance of a recurring series using the changes in req.
// req's start/end are the edited instance's times; they are translated onto the series
// anchor (shifted by however far the instance moved) so the duration stays the
// instance's own span. The series' end bound (EndDate/MaxOccurrences) is carried over.
// Used for "all" scope edits.
func (r *RecurringEventRepository) UpdateAll(ctx context.Context, recurringEventID, ownerID uuid.UUID, instanceStartTime time.Time, req model.UpdateRecurrenceRequest) (*model.RecurringEvent, error) {
	parent, err := r.GetByID(ctx, recurringEventID, ownerID)
	if err != nil {
		return nil, err
	}
	updateReq := model.UpdateRecurringEventRequest{
		Title:       req.Title,
		Description: req.Description,
		Location:    req.Location,
		Attendees:   req.Attendees,
		Reminders:   req.Reminders,
		AllDay:      req.AllDay,
		Timezone:    req.Timezone,
		CategoryID:  req.CategoryID,
		Visibility:  req.Visibility,
		// Update treats nil as "clear" for these, so pass the current bound through —
		// otherwise any all-scope edit would silently make a bounded series unbounded.
		EndDate:        parent.EndDate,
		MaxOccurrences: parent.MaxOccurrences,
	}

	newInstStart := instanceStartTime
	if req.StartTime != nil {
		newInstStart = *req.StartTime
	}
	shifted := parent.StartTime.Add(newInstStart.Sub(instanceStartTime))
	if req.StartTime != nil {
		updateReq.StartTime = &shifted
	}
	if req.EndTime != nil {
		// Update derives Duration as EndTime - StartTime (anchor), so express the
		// instance's end relative to the shifted anchor, not as the instance's own date.
		end := shifted.Add(req.EndTime.Sub(newInstStart))
		updateReq.EndTime = &end
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

	// One bad rule must not block window extension for the rest: attempt every
	// rule and report the failures together.
	var errs []error
	for i := range rules {
		rec := &rules[i]
		from := rec.GeneratedUntil
		if err := r.generateWindow(ctx, rec, from, horizon); err != nil {
			errs = append(errs, fmt.Errorf("recurring rule %s: %w", rec.ID, err))
		}
	}
	return errors.Join(errs...)
}

func (r *RecurringEventRepository) generateWindow(ctx context.Context, rec *model.RecurringEvent, from, until time.Time) error {
	occurrences := nextOccurrences(rec, from, until)
	duration := time.Duration(rec.Duration)

	remindersJSON := marshalReminders(rec.Reminders)
	for _, start := range occurrences {
		end := start.Add(duration)
		_, err := r.pool.Exec(ctx, `
			INSERT INTO events
				(owner_id, calendar_id, title, description, location,
				 start_time, end_time, attendees, reminders, all_day, timezone, category_id, recurring_event_id, visibility)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
			ON CONFLICT (recurring_event_id, start_time) WHERE recurring_event_id IS NOT NULL DO NOTHING`,
			rec.OwnerID, rec.CalendarID, rec.Title, rec.Description, rec.Location,
			start, end, rec.Attendees, remindersJSON, rec.AllDay, rec.Timezone, rec.CategoryID, rec.ID, rec.Visibility,
		)
		if err != nil {
			return err
		}
	}

	if len(occurrences) > 0 && len(rec.Attendees) > 0 && rec.SendInvitations {
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

	// Advance even when the window held no occurrences, so an ended (or sparse) series
	// isn't re-scanned on every scheduler run. Never move it back: a view may have
	// extended the series further (ExtendThrough) than a concurrent run reaches.
	_, err := r.pool.Exec(ctx,
		`UPDATE recurring_events SET generated_until = GREATEST(generated_until, $1) WHERE id=$2`, until, rec.ID)
	return err
}

// seriesLocation is the zone a series is stepped in. All-day instances are stored as
// UTC midnight (the UI and ICS import both do this), so they must be stepped in UTC:
// rebuilding UTC midnight as wall-clock time in, say, America/Los_Angeles lands weekly
// day-of-week matches on the previous day and lets DST move the UTC date.
func seriesLocation(rec *model.RecurringEvent) *time.Location {
	if rec.AllDay {
		return time.UTC
	}
	loc, err := time.LoadLocation(rec.Timezone)
	if err != nil {
		return time.UTC
	}
	return loc
}

// remainingOccurrences returns how much of rec's MaxOccurrences budget is left for
// occurrences at or after pivot (nil when the series is uncapped), so a series forked at
// pivot keeps the original total instead of restarting its count. Never less than 1:
// the instance at pivot is being edited, so it exists.
func remainingOccurrences(rec *model.RecurringEvent, pivot time.Time) *int {
	if rec.MaxOccurrences == nil {
		return nil
	}
	remaining := max(*rec.MaxOccurrences-occurrencesBefore(rec, pivot), 1)
	return &remaining
}

// maxWalk bounds walk as a guard against a runaway loop (27 years of a daily series).
const maxWalk = 10000

// walk calls visit with rec's occurrences in order, numbered from 1 (the anchor), until
// visit returns false. Unlike nextOccurrences it ignores end bounds and exdates.
func walk(rec *model.RecurringEvent, visit func(n int, t time.Time) bool) {
	loc := seriesLocation(rec)
	anchor := rec.StartTime.In(loc)
	h, m, s, ns := anchor.Hour(), anchor.Minute(), anchor.Second(), anchor.Nanosecond()
	cur := rec.StartTime
	for n := 1; n <= maxWalk && visit(n, cur); n++ {
		cur = step(rec, cur, loc, h, m, s, ns)
	}
}

// occurrencesBefore counts rec's occurrences strictly before t.
func occurrencesBefore(rec *model.RecurringEvent, t time.Time) int {
	count := 0
	walk(rec, func(_ int, cur time.Time) bool {
		if !cur.Before(t) {
			return false
		}
		count++
		return true
	})
	return count
}

// remapOccurrences maps each of ts that is an occurrence of from (keyed by UnixMicro) to
// the occurrence of to at the same position in the series, less skip, for a series
// forked at from's occurrence skip+1. Mapping by position, not by shifting a fixed
// duration, keeps a time on its occurrence when an edit moves the series across a DST
// change, onto fixed weekdays, or between all-day and timed. Times that aren't
// occurrences of from, or are among its first skip, are left out.
func remapOccurrences(from, to *model.RecurringEvent, skip int, ts []time.Time) map[int64]time.Time {
	out := map[int64]time.Time{}
	if len(ts) == 0 {
		return out
	}
	want := make(map[int64]bool, len(ts))
	last := ts[0]
	for _, t := range ts {
		want[t.UnixMicro()] = true
		if t.After(last) {
			last = t
		}
	}
	keyAt := map[int]int64{} // position in to's series -> key in ts
	maxPos := 0
	walk(from, func(n int, cur time.Time) bool {
		if cur.After(last) {
			return false
		}
		if k := cur.UnixMicro(); want[k] && n > skip {
			keyAt[n-skip] = k
			maxPos = n - skip
		}
		return true
	})
	walk(to, func(n int, cur time.Time) bool {
		if n > maxPos {
			return false
		}
		if k, ok := keyAt[n]; ok {
			out[k] = cur
		}
		return true
	})
	return out
}

// remapExdates returns from's exdates carried over to to (see remapOccurrences).
func remapExdates(from, to *model.RecurringEvent, skip int) []time.Time {
	moved := remapOccurrences(from, to, skip, from.Exdates)
	out := []time.Time{}
	for _, t := range from.Exdates {
		if nt, ok := moved[t.UnixMicro()]; ok {
			out = append(out, nt)
		}
	}
	return out
}

// nextOccurrences returns all occurrence start times in (from, until].
// Anchors from StartTime to preserve schedule alignment after updates.
// Steps dates in the event's timezone so wall-clock time is preserved across DST changes.
// Exdates are skipped but still counted toward MaxOccurrences.
func nextOccurrences(rec *model.RecurringEvent, from, until time.Time) []time.Time {
	loc := seriesLocation(rec)
	// Keyed at microsecond precision: exdates round-trip through timestamptz.
	excluded := make(map[int64]bool, len(rec.Exdates))
	for _, t := range rec.Exdates {
		excluded[t.UnixMicro()] = true
	}

	// Wall-clock components of the anchor, used to reconstruct each occurrence.
	anchor := rec.StartTime.In(loc)
	h, m, s, ns := anchor.Hour(), anchor.Minute(), anchor.Second(), anchor.Nanosecond()

	var results []time.Time
	current := rec.StartTime
	count := 1 // rec.StartTime itself is occurrence #1
	for !current.After(from) {
		current = step(rec, current, loc, h, m, s, ns)
		count++
	}
	for !current.After(until) {
		if rec.EndDate != nil && current.After(*rec.EndDate) {
			break
		}
		if rec.MaxOccurrences != nil && count > *rec.MaxOccurrences {
			break
		}
		if !excluded[current.UnixMicro()] {
			results = append(results, current)
		}
		current = step(rec, current, loc, h, m, s, ns)
		count++
	}
	return results
}

// step advances t by one recurrence interval, reconstructing the wall-clock time
// in loc so that DST transitions don't shift the displayed hour. Weekly steps with
// explicit days honor Interval by skipping whole weeks (WKST = Monday). Monthly and
// yearly steps anchor on StartTime's day-of-month and clamp to the last valid day of
// the target month, so e.g. monthly-on-31 lands on Feb 29 (and Feb 29 yearly → Feb 28).
func step(rec *model.RecurringEvent, t time.Time, loc *time.Location, h, m, s, ns int) time.Time {
	local := t.In(loc)
	n := rec.Interval
	rebuild := func(d time.Time) time.Time {
		return time.Date(d.Year(), d.Month(), d.Day(), h, m, s, ns, loc).UTC()
	}
	matches := func(d time.Time) bool {
		for _, wd := range rec.DaysOfWeek {
			if int(d.Weekday()) == wd {
				return true
			}
		}
		return false
	}
	switch rec.Frequency {
	case "daily":
		return rebuild(local.AddDate(0, 0, n))
	case "weekly":
		if len(rec.DaysOfWeek) == 0 {
			return rebuild(local.AddDate(0, 0, 7*n))
		}
		// A later matching day in the current week (WKST = Monday) comes first.
		isoDow := (int(local.Weekday()) + 6) % 7 // Mon=0 … Sun=6
		for off := 1; off <= 6-isoDow; off++ {
			if c := local.AddDate(0, 0, off); matches(c) {
				return rebuild(c)
			}
		}
		// Otherwise skip Interval whole weeks and take the first matching day.
		nextWeek := local.AddDate(0, 0, -isoDow+7*n)
		for off := 0; off <= 6; off++ {
			if c := nextWeek.AddDate(0, 0, off); matches(c) {
				return rebuild(c)
			}
		}
		return rebuild(nextWeek)
	case "monthly":
		anchor := rec.StartTime.In(loc)
		total := int(local.Month()) - 1 + n
		return clampDate(local.Year()+total/12, time.Month(total%12+1), anchor.Day(), h, m, s, ns, loc)
	case "yearly":
		anchor := rec.StartTime.In(loc)
		return clampDate(local.Year()+n, anchor.Month(), anchor.Day(), h, m, s, ns, loc)
	default:
		return rebuild(local.AddDate(0, 0, 1))
	}
}

// clampDate builds a UTC instant for year/month/day at the given wall-clock time in
// loc, clamping day down to the month's last day when it overflows (e.g. Feb 31 → 29).
func clampDate(year int, month time.Month, day, h, m, s, ns int, loc *time.Location) time.Time {
	if last := daysInMonth(year, month); day > last {
		day = last
	}
	return time.Date(year, month, day, h, m, s, ns, loc).UTC()
}

// daysInMonth returns the number of days in month (day 0 of the following month).
func daysInMonth(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}
