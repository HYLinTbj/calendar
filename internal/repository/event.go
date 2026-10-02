package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hylin/calendar/internal/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type EventRepository struct {
	pool *pgxpool.Pool
}

func NewEventRepository(pool *pgxpool.Pool) *EventRepository {
	return &EventRepository{pool: pool}
}

const eventCols = `id, owner_id, calendar_id, title, description, location, start_time, end_time, attendees, reminders, all_day, timezone, category_id, recurring_event_id, visibility, created_at, updated_at, detached_from, original_start`

// eventColsJ is for queries that JOIN calendars c ON c.id = e.calendar_id.
// Appends c.owner_id so callers can apply privacy masking.
const eventColsJ = `e.id, e.owner_id, e.calendar_id, e.title, e.description, e.location, e.start_time, e.end_time, e.attendees, e.reminders, e.all_day, e.timezone, e.category_id, e.recurring_event_id, e.visibility, e.created_at, e.updated_at, e.detached_from, e.original_start, c.owner_id`

func scanEvent(row interface{ Scan(...any) error }, e *model.Event) error {
	var remindersRaw []byte
	err := row.Scan(&e.ID, &e.OwnerID, &e.CalendarID, &e.Title, &e.Description, &e.Location,
		&e.StartTime, &e.EndTime, &e.Attendees, &remindersRaw, &e.AllDay, &e.Timezone,
		&e.CategoryID, &e.RecurringEventID, &e.Visibility, &e.CreatedAt, &e.UpdatedAt, &e.DetachedFrom, &e.OriginalStart)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(remindersRaw, &e.Reminders); err != nil {
		e.Reminders = []model.Reminder{}
	}
	return nil
}

func scanEventJ(row interface{ Scan(...any) error }, e *model.Event) (uuid.UUID, error) {
	var remindersRaw []byte
	var calOwnerID uuid.UUID
	err := row.Scan(&e.ID, &e.OwnerID, &e.CalendarID, &e.Title, &e.Description, &e.Location,
		&e.StartTime, &e.EndTime, &e.Attendees, &remindersRaw, &e.AllDay, &e.Timezone,
		&e.CategoryID, &e.RecurringEventID, &e.Visibility, &e.CreatedAt, &e.UpdatedAt, &e.DetachedFrom, &e.OriginalStart, &calOwnerID)
	if err != nil {
		return uuid.UUID{}, err
	}
	if err := json.Unmarshal(remindersRaw, &e.Reminders); err != nil {
		e.Reminders = []model.Reminder{}
	}
	return calOwnerID, nil
}

// maskIfPrivate replaces sensitive fields with "Busy" for shared-calendar viewers
// who are neither the event owner nor the calendar owner.
func maskIfPrivate(e *model.Event, requesterID, calOwnerID uuid.UUID) {
	if e.Visibility != "private" {
		return
	}
	if e.OwnerID == requesterID || calOwnerID == requesterID {
		return
	}
	e.Title = "Busy"
	e.Description = ""
	e.Location = ""
	e.Attendees = []string{}
	e.Reminders = []model.Reminder{}
	e.CategoryID = nil
	e.RecurringEventID = nil
	e.DetachedFrom, e.OriginalStart = nil, nil
}

// calAccessFragJ is for queries that already JOIN calendars c ON c.id = e.calendar_id.
func calAccessFragJ(n int) string {
	p := "$" + itoa(n)
	return `(e.owner_id = ` + p + `
		OR c.owner_id = ` + p + `
		OR e.calendar_id IN (SELECT calendar_id FROM calendar_shares WHERE shared_with_user_id = ` + p + `))`
}

// eventWriteAccess is the SQL condition (on the events row, unaliased) for whether user
// $n may change or delete it: the owner of its calendar may, and so may its author while
// they still have edit access to that calendar — revoking or downgrading a share revokes
// it. Editors can't change each other's or the owner's events.
func eventWriteAccess(n int) string {
	p := "$" + itoa(n)
	return `(EXISTS (SELECT 1 FROM calendars c WHERE c.id = events.calendar_id AND c.owner_id = ` + p + `)
		OR (events.owner_id = ` + p + ` AND EXISTS (
			SELECT 1 FROM calendar_shares s
			WHERE s.calendar_id = events.calendar_id AND s.shared_with_user_id = ` + p + ` AND s.permission = 'edit')))`
}

func marshalReminders(reminders []model.Reminder) []byte {
	if reminders == nil {
		reminders = []model.Reminder{}
	}
	data, _ := json.Marshal(reminders)
	return data
}

func (r *EventRepository) Create(ctx context.Context, ownerID, calendarID uuid.UUID, req model.CreateEventRequest) (*model.Event, error) {
	attendees := req.Attendees
	if attendees == nil {
		attendees = []string{}
	}
	tz := req.Timezone
	if tz == "" {
		tz = "UTC"
	}
	vis := req.Visibility
	if vis == "" {
		vis = "public"
	}
	var e model.Event
	err := scanEvent(r.pool.QueryRow(ctx, `
		INSERT INTO events (owner_id, calendar_id, title, description, location, start_time, end_time, attendees, reminders, all_day, timezone, category_id, visibility)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING `+eventCols,
		ownerID, calendarID, req.Title, req.Description, req.Location,
		req.StartTime, req.EndTime, attendees, marshalReminders(req.Reminders), req.AllDay, tz, req.CategoryID, vis), &e)
	return &e, err
}

func (r *EventRepository) GetByID(ctx context.Context, id, requesterID uuid.UUID) (*model.Event, error) {
	var e model.Event
	calOwnerID, err := scanEventJ(r.pool.QueryRow(ctx,
		`SELECT `+eventColsJ+` FROM events e JOIN calendars c ON c.id = e.calendar_id WHERE e.id = $1 AND `+calAccessFragJ(2),
		id, requesterID), &e)
	if err != nil {
		return nil, err
	}
	maskIfPrivate(&e, requesterID, calOwnerID)
	return &e, nil
}

// List returns events starting within [from, to].
func (r *EventRepository) List(ctx context.Context, ownerID uuid.UUID, calendarID *uuid.UUID, from, to *time.Time) ([]model.Event, error) {
	return r.list(ctx, ownerID, calendarID, from, to, false)
}

// ListOverlapping returns events overlapping [from, to] — including ones that started
// before from, such as a multi-day all-day event, which a calendar view must still draw.
func (r *EventRepository) ListOverlapping(ctx context.Context, ownerID uuid.UUID, calendarID *uuid.UUID, from, to *time.Time) ([]model.Event, error) {
	return r.list(ctx, ownerID, calendarID, from, to, true)
}

func (r *EventRepository) list(ctx context.Context, ownerID uuid.UUID, calendarID *uuid.UUID, from, to *time.Time, overlap bool) ([]model.Event, error) {
	query := `SELECT ` + eventColsJ + ` FROM events e JOIN calendars c ON c.id = e.calendar_id WHERE ` + calAccessFragJ(1)
	args := []any{ownerID}
	i := 2

	if calendarID != nil {
		query += ` AND e.calendar_id = $` + itoa(i)
		args = append(args, calendarID)
		i++
	}
	if from != nil {
		if overlap {
			query += ` AND e.end_time >= $` + itoa(i)
		} else {
			query += ` AND e.start_time >= $` + itoa(i)
		}
		args = append(args, from)
		i++
	}
	if to != nil {
		query += ` AND e.start_time <= $` + itoa(i)
		args = append(args, to)
		i++
	}
	query += ` ORDER BY e.start_time ASC`

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := []model.Event{}
	for rows.Next() {
		var e model.Event
		calOwnerID, err := scanEventJ(rows, &e)
		if err != nil {
			return nil, err
		}
		maskIfPrivate(&e, ownerID, calOwnerID)
		events = append(events, e)
	}
	return events, rows.Err()
}

// GetBusySlots returns the merged busy intervals for targetUserID within [from, to),
// visible to requesterID. Returns nil slots (not an error) when the requester has no
// access to the target's calendars.
func (r *EventRepository) GetBusySlots(ctx context.Context, requesterID, targetUserID uuid.UUID, from, to time.Time) ([]model.TimeSlot, bool, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT e.start_time, e.end_time
		FROM events e
		JOIN calendars c ON c.id = e.calendar_id
		WHERE c.owner_id = $1
		  AND (
		    $2 = $1
		    OR EXISTS (
		        SELECT 1 FROM calendar_shares cs
		        WHERE cs.calendar_id = c.id AND cs.shared_with_user_id = $2
		    )
		  )
		  AND e.end_time > $3
		  AND e.start_time < $4
		ORDER BY e.start_time ASC`,
		targetUserID, requesterID, from, to)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	var slots []model.TimeSlot
	for rows.Next() {
		var s model.TimeSlot
		if err := rows.Scan(&s.Start, &s.End); err != nil {
			return nil, false, err
		}
		slots = append(slots, s)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}

	// If no rows and requester != target, we can't distinguish "no events" from "no access".
	// Re-check whether access exists at all.
	hasAccess := requesterID == targetUserID || len(slots) > 0
	if !hasAccess {
		var count int
		err = r.pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM calendar_shares cs
			JOIN calendars c ON c.id = cs.calendar_id
			WHERE c.owner_id = $1 AND cs.shared_with_user_id = $2`,
			targetUserID, requesterID).Scan(&count)
		if err != nil {
			return nil, false, err
		}
		hasAccess = count > 0
	}

	return mergeSlots(slots), hasAccess, nil
}

func mergeSlots(slots []model.TimeSlot) []model.TimeSlot {
	if len(slots) == 0 {
		return []model.TimeSlot{}
	}
	merged := []model.TimeSlot{slots[0]}
	for _, s := range slots[1:] {
		last := &merged[len(merged)-1]
		if !s.Start.After(last.End) {
			if s.End.After(last.End) {
				last.End = s.End
			}
		} else {
			merged = append(merged, s)
		}
	}
	return merged
}

func (r *EventRepository) Search(ctx context.Context, ownerID uuid.UUID, q string, calendarID *uuid.UUID) ([]model.Event, error) {
	tsq := "plainto_tsquery('english', $2)"
	// Private events are excluded from search results entirely for non-owners/non-calendar-owners
	// to avoid leaking that a matching private event exists.
	query := `SELECT ` + eventColsJ + ` FROM events e JOIN calendars c ON c.id = e.calendar_id
		WHERE ` + calAccessFragJ(1) + `
		  AND e.search_vector @@ ` + tsq + `
		  AND (e.visibility = 'public' OR e.owner_id = $1 OR c.owner_id = $1)`
	args := []any{ownerID, q}
	i := 3

	if calendarID != nil {
		query += ` AND e.calendar_id = $` + itoa(i)
		args = append(args, calendarID)
		i++
	}
	query += ` ORDER BY ts_rank(e.search_vector, ` + tsq + `) DESC LIMIT 50`

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := []model.Event{}
	for rows.Next() {
		var e model.Event
		_, err := scanEventJ(rows, &e)
		if err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

func (r *EventRepository) Update(ctx context.Context, id, ownerID uuid.UUID, req model.UpdateEventRequest) (*model.Event, error) {
	current, err := r.GetByID(ctx, id, ownerID)
	if err != nil {
		return nil, err
	}
	recurringEventID, occurrence := current.RecurringEventID, current.StartTime
	before := *current
	if req.CalendarID != nil {
		current.CalendarID = *req.CalendarID
	}
	if req.Title != nil {
		current.Title = *req.Title
	}
	if req.Description != nil {
		current.Description = *req.Description
	}
	if req.Location != nil {
		current.Location = *req.Location
	}
	if req.StartTime != nil {
		current.StartTime = *req.StartTime
	}
	if req.EndTime != nil {
		current.EndTime = *req.EndTime
	}
	if req.Attendees != nil {
		current.Attendees = req.Attendees
	}
	if req.Reminders != nil {
		current.Reminders = req.Reminders
	}
	if req.AllDay != nil {
		current.AllDay = *req.AllDay
	}
	if req.Timezone != nil {
		current.Timezone = *req.Timezone
	}
	if req.CategoryID.Set {
		current.CategoryID = req.CategoryID.Value
	}
	if req.Visibility != nil {
		current.Visibility = *req.Visibility
	}

	// Saving a series instance without changing anything (the modal's Save, a Log-view
	// re-save) isn't an edit: leave it linked rather than detach it below. It still takes
	// write access, as an edit would.
	if recurringEventID != nil && sameEventFields(&before, current) {
		var ok bool
		if err := r.pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM events WHERE id = $1 AND `+eventWriteAccess(2)+`)`, id, ownerID).Scan(&ok); err != nil {
			return nil, err
		}
		if !ok {
			return nil, pgx.ErrNoRows
		}
		return current, nil
	}

	// Editing a series instance on its own makes it an exception: detach it into a
	// standalone event and add its occurrence time to the series' exdates, so a later
	// series-wide edit (which regenerates future instances) neither overwrites nor
	// duplicates it. A linked instance's start_time is always its occurrence time,
	// since any edit detaches it. The exception keeps a link to its series and that
	// occurrence (detached_from, original_start; SET sees the pre-update row) so
	// deleting the series still removes it. The exdate is only recorded if the update
	// applied.
	var e model.Event
	err = scanEvent(r.pool.QueryRow(ctx, `
		WITH upd AS (
			UPDATE events
			SET calendar_id=$1, title=$2, description=$3, location=$4,
			    start_time=$5, end_time=$6, attendees=$7, reminders=$8, all_day=$9, timezone=$10, category_id=$11, visibility=$12,
			    detached_from = COALESCE(detached_from, recurring_event_id),
			    original_start = CASE WHEN recurring_event_id IS NOT NULL THEN start_time ELSE original_start END,
			    recurring_event_id=NULL, updated_at=NOW()
			WHERE id=$13 AND `+eventWriteAccess(14)+`
			RETURNING `+eventCols+`
		), ex AS (
			UPDATE recurring_events SET exdates = array_append(exdates, $16), updated_at = NOW()
			WHERE id = $15 AND owner_id = $14 AND EXISTS (SELECT 1 FROM upd)
		)
		SELECT `+eventCols+` FROM upd`,
		current.CalendarID, current.Title, current.Description, current.Location,
		current.StartTime, current.EndTime, current.Attendees, marshalReminders(current.Reminders),
		current.AllDay, current.Timezone, current.CategoryID, current.Visibility,
		id, ownerID, recurringEventID, occurrence), &e)
	return &e, err
}

// sameEventFields reports whether a and b agree on everything Update can change.
func sameEventFields(a, b *model.Event) bool {
	return a.CalendarID == b.CalendarID && a.Title == b.Title && a.Description == b.Description &&
		a.Location == b.Location && a.StartTime.Equal(b.StartTime) && a.EndTime.Equal(b.EndTime) &&
		slices.Equal(a.Attendees, b.Attendees) && slices.Equal(a.Reminders, b.Reminders) &&
		a.AllDay == b.AllDay && a.Timezone == b.Timezone && a.Visibility == b.Visibility &&
		((a.CategoryID == nil && b.CategoryID == nil) ||
			(a.CategoryID != nil && b.CategoryID != nil && *a.CategoryID == *b.CategoryID))
}

// Delete removes an event, returning pgx.ErrNoRows if the caller may not (see
// eventWriteAccess) or there's no such event.
// Deleting a series instance records its occurrence time in the series' exdates so the
// series never regenerates it.
func (r *EventRepository) Delete(ctx context.Context, id, ownerID uuid.UUID) error {
	var deleted int
	err := r.pool.QueryRow(ctx, `
		WITH del AS (
			DELETE FROM events WHERE id = $1 AND `+eventWriteAccess(2)+`
			RETURNING recurring_event_id, start_time
		), ex AS (
			UPDATE recurring_events r SET exdates = array_append(r.exdates, del.start_time), updated_at = NOW()
			FROM del WHERE r.id = del.recurring_event_id
		)
		SELECT count(*) FROM del`,
		id, ownerID).Scan(&deleted)
	if err != nil {
		return err
	}
	if deleted == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// statsAreaCols are the columns, after a row's category_id, that scanStatsRow reads
// into a model.AreaInfo, from the row's category joined as c and its group as g
// (statsAreaJoin). statsAreaGroupBy groups by them.
const statsAreaCols = `COALESCE(c.name, ''), COALESCE(c.code, ''), COALESCE(c.color, ''),
		       COALESCE(c.weekly_target_minutes, 0), c.group_id, COALESCE(g.name, ''), COALESCE(g.color, '')`

const statsAreaGroupBy = `c.name, c.code, c.color, c.weekly_target_minutes, c.group_id, g.name, g.color`

// statsAreaJoin joins the category (c) and category group (g) of the rows aliased t.
func statsAreaJoin(t string) string {
	return `LEFT JOIN categories c ON c.id = ` + t + `.category_id
		LEFT JOIN category_groups g ON g.id = c.group_id`
}

// statsTraceWindow keeps the traces t (of owner $1) whose day starts, at local
// midnight in tz $4, within [$2, $3).
const statsTraceWindow = `t.owner_id = $1
		  -- The local days holding from and to: a bound the (owner_id, day) index can use.
		  AND t.day >= ($2::timestamptz AT TIME ZONE $4)::date
		  AND t.day <= ($3::timestamptz AT TIME ZONE $4)::date
		  AND (t.day::timestamp AT TIME ZONE $4) >= $2
		  AND (t.day::timestamp AT TIME ZONE $4) < $3`

// statsEventMinutes is the minutes of a stats group of events e, rounded per group.
const statsEventMinutes = `SUM(EXTRACT(EPOCH FROM (e.end_time - e.start_time)) / 60)::int`

// scanStatsRow scans a row's category_id and statsAreaCols, then extra.
func scanStatsRow(rows pgx.Rows, extra ...any) (model.AreaInfo, error) {
	var a model.AreaInfo
	dest := append([]any{&a.AreaID, &a.AreaName, &a.AreaCode, &a.AreaColor,
		&a.WeeklyTargetMinutes, &a.GroupID, &a.GroupName, &a.GroupColor}, extra...)
	if err := rows.Scan(dest...); err != nil {
		return a, err
	}
	if a.AreaID == nil {
		a.AreaName = "Uncategorized"
	}
	return a, nil
}

// areaKey keys stats entries by Area: time with no Area shares one entry.
func areaKey(id *uuid.UUID) string {
	if id == nil {
		return "none"
	}
	return id.String()
}

// compareAreas orders stats entries by name, with Uncategorized last. Each query
// gives that order on its own, but an Area seen only among traces would otherwise
// come after Uncategorized.
func compareAreas(a, b model.AreaInfo) int {
	if (a.AreaID == nil) != (b.AreaID == nil) {
		if a.AreaID == nil {
			return 1
		}
		return -1
	}
	return strings.Compare(strings.ToLower(a.AreaName), strings.ToLower(b.AreaName))
}

// Stats aggregates time spent over [from, to), grouped by Area (category): minutes
// of events starting in the window (each one's end_time - start_time; all-day events
// are excluded, they carry no meaningful duration), broken down by sub-activity
// (event title), plus traces whose day starts (at local midnight in tz, an IANA zone
// name) in it. Time with no category (NULL, or a since-deleted one) collects under a
// single "Uncategorized" entry. Traces have no clock time, so a day's traces count
// in whichever window holds its start; "[week start, now)" then includes today's
// and "[now, week end)" doesn't, so the two never count them twice.
func (r *EventRepository) Stats(ctx context.Context, ownerID uuid.UUID, from, to time.Time, tz string) (*model.TimeStats, error) {
	stats := &model.TimeStats{From: from, To: to, Areas: []model.AreaStat{}}
	pos := map[string]int{} // areaKey -> index into stats.Areas
	// entry returns the stats entry for an Area, adding it on first sight.
	entry := func(info model.AreaInfo) *model.AreaStat {
		i, ok := pos[areaKey(info.AreaID)]
		if !ok {
			stats.Areas = append(stats.Areas, model.AreaStat{AreaInfo: info, SubActivities: []model.SubActivityStat{}})
			i = len(stats.Areas) - 1
			pos[areaKey(info.AreaID)] = i
		}
		return &stats.Areas[i]
	}

	rows, err := r.pool.Query(ctx, `
		SELECT e.category_id, `+statsAreaCols+`, e.title, `+statsEventMinutes+`
		FROM events e
		`+statsAreaJoin("e")+`
		WHERE e.owner_id = $1 AND e.all_day = false
		  AND e.start_time >= $2 AND e.start_time < $3
		GROUP BY e.category_id, `+statsAreaGroupBy+`, e.title
		ORDER BY c.name NULLS LAST, e.title`,
		ownerID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var sub string
		var minutes int
		info, err := scanStatsRow(rows, &sub, &minutes)
		if err != nil {
			return nil, err
		}
		e := entry(info)
		e.EventMinutes += minutes
		e.TotalMinutes += minutes
		if sub != "" {
			e.SubActivities = append(e.SubActivities, model.SubActivityStat{Name: sub, Minutes: minutes})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// A trace's note is not a sub-activity: notes are free text, so they'd only
	// split the per-title breakdown into one-off rows.
	trows, err := r.pool.Query(ctx, `
		SELECT t.category_id, `+statsAreaCols+`, SUM(t.minutes)::int
		FROM time_traces t
		`+statsAreaJoin("t")+`
		WHERE `+statsTraceWindow+`
		GROUP BY t.category_id, `+statsAreaGroupBy,
		ownerID, from, to, tz)
	if err != nil {
		return nil, tzErr(err)
	}
	defer trows.Close()
	for trows.Next() {
		var minutes int
		info, err := scanStatsRow(trows, &minutes)
		if err != nil {
			return nil, err
		}
		e := entry(info)
		e.TraceMinutes += minutes
		e.TotalMinutes += minutes
	}
	if err := trows.Err(); err != nil {
		return nil, tzErr(err)
	}
	slices.SortStableFunc(stats.Areas, func(a, b model.AreaStat) int { return compareAreas(a.AreaInfo, b.AreaInfo) })
	return stats, nil
}

// weekOf is the SQL for the first day of the week holding date d, for weeks
// starting on weekday ws (Sun=0 … Sat=6).
func weekOf(d, ws string) string {
	return `(` + d + ` - ((EXTRACT(DOW FROM ` + d + `)::int - ` + ws + ` + 7) % 7))`
}

// WeeklyStats splits Stats into weeks: each Area's total minutes over [from, to)
// per week of local days in tz, the weeks starting on weekday weekStart (Sun=0 …
// Sat=6). An event counts in the week its start falls in, a trace in the week
// holding its day, and event minutes are rounded per title as Stats rounds them, so
// a week's minutes are what Stats gives over the same span. The first and last
// weeks are partial when from and to aren't week boundaries.
func (r *EventRepository) WeeklyStats(ctx context.Context, ownerID uuid.UUID, from, to time.Time, tz string, weekStart int) (*model.WeeklyStats, error) {
	stats := &model.WeeklyStats{From: from, To: to, TZ: tz, WeekStart: weekStart, Weeks: []model.Date{}, Areas: []model.AreaWeekly{}}
	if !from.Before(to) {
		return stats, nil
	}

	// The weeks holding the range's first and last instants, in Postgres' own zone
	// rules: the same ones that bucket the rows below.
	var first, last time.Time
	if err := r.pool.QueryRow(ctx,
		`SELECT `+weekOf(`($1::timestamptz AT TIME ZONE $3)::date`, `$4::int`)+`,
		        `+weekOf(`(($2::timestamptz - interval '1 microsecond') AT TIME ZONE $3)::date`, `$4::int`),
		from, to, tz, weekStart,
	).Scan(&first, &last); err != nil {
		return nil, tzErr(err)
	}
	week := map[string]int{} // week's first day -> index into stats.Weeks
	for d := first; !d.After(last); d = d.AddDate(0, 0, 7) {
		day := d.Format(time.DateOnly)
		week[day] = len(stats.Weeks)
		stats.Weeks = append(stats.Weeks, model.Date(day))
	}

	pos := map[string]int{} // areaKey -> index into stats.Areas
	// add counts minutes for an Area in the week starting on day.
	add := func(info model.AreaInfo, day time.Time, minutes int) error {
		w, ok := week[day.Format(time.DateOnly)]
		if !ok {
			return fmt.Errorf("week %s is outside %s – %s", day.Format(time.DateOnly), first.Format(time.DateOnly), last.Format(time.DateOnly))
		}
		i, ok := pos[areaKey(info.AreaID)]
		if !ok {
			stats.Areas = append(stats.Areas, model.AreaWeekly{AreaInfo: info, Minutes: make([]int, len(stats.Weeks))})
			i = len(stats.Areas) - 1
			pos[areaKey(info.AreaID)] = i
		}
		stats.Areas[i].Minutes[w] += minutes
		return nil
	}

	rows, err := r.pool.Query(ctx, `
		SELECT e.category_id, `+statsAreaCols+`,
		       `+weekOf(`(e.start_time AT TIME ZONE $4)::date`, `$5::int`)+` AS week,
		       `+statsEventMinutes+`
		FROM events e
		`+statsAreaJoin("e")+`
		WHERE e.owner_id = $1 AND e.all_day = false
		  AND e.start_time >= $2 AND e.start_time < $3
		GROUP BY e.category_id, `+statsAreaGroupBy+`, week, e.title`,
		ownerID, from, to, tz, weekStart)
	if err != nil {
		return nil, tzErr(err)
	}
	defer rows.Close()
	for rows.Next() {
		var day time.Time
		var minutes int
		info, err := scanStatsRow(rows, &day, &minutes)
		if err != nil {
			return nil, err
		}
		if err := add(info, day, minutes); err != nil {
			return nil, err
		}
	}
	if err := rows.Err(); err != nil {
		return nil, tzErr(err)
	}

	trows, err := r.pool.Query(ctx, `
		SELECT t.category_id, `+statsAreaCols+`,
		       `+weekOf(`t.day`, `$5::int`)+` AS week, SUM(t.minutes)::int
		FROM time_traces t
		`+statsAreaJoin("t")+`
		WHERE `+statsTraceWindow+`
		GROUP BY t.category_id, `+statsAreaGroupBy+`, week`,
		ownerID, from, to, tz, weekStart)
	if err != nil {
		return nil, tzErr(err)
	}
	defer trows.Close()
	for trows.Next() {
		var day time.Time
		var minutes int
		info, err := scanStatsRow(trows, &day, &minutes)
		if err != nil {
			return nil, err
		}
		if err := add(info, day, minutes); err != nil {
			return nil, err
		}
	}
	if err := trows.Err(); err != nil {
		return nil, tzErr(err)
	}
	slices.SortStableFunc(stats.Areas, func(a, b model.AreaWeekly) int { return compareAreas(a.AreaInfo, b.AreaInfo) })
	return stats, nil
}

// ErrInvalidTimeZone is returned by Stats and WeeklyStats when Postgres doesn't know their tz,
// which can happen for a name Go's own tz database accepts.
var ErrInvalidTimeZone = errors.New("invalid time zone")

// tzErr maps Postgres' "time zone not recognized" (invalid_parameter_value) to
// ErrInvalidTimeZone.
func tzErr(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "22023" {
		return ErrInvalidTimeZone
	}
	return err
}

func itoa(i int) string {
	return strconv.Itoa(i)
}
