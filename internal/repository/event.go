package repository

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/hylin/calendar/internal/model"
	"github.com/jackc/pgx/v5/pgxpool"
)

type EventRepository struct {
	pool *pgxpool.Pool
}

func NewEventRepository(pool *pgxpool.Pool) *EventRepository {
	return &EventRepository{pool: pool}
}

const eventCols = `id, owner_id, calendar_id, title, description, location, start_time, end_time, attendees, reminders, all_day, timezone, category_id, recurring_event_id, visibility, created_at, updated_at`

// eventColsJ is for queries that JOIN calendars c ON c.id = e.calendar_id.
// Appends c.owner_id so callers can apply privacy masking.
const eventColsJ = `e.id, e.owner_id, e.calendar_id, e.title, e.description, e.location, e.start_time, e.end_time, e.attendees, e.reminders, e.all_day, e.timezone, e.category_id, e.recurring_event_id, e.visibility, e.created_at, e.updated_at, c.owner_id`

func scanEvent(row interface{ Scan(...any) error }, e *model.Event) error {
	var remindersRaw []byte
	err := row.Scan(&e.ID, &e.OwnerID, &e.CalendarID, &e.Title, &e.Description, &e.Location,
		&e.StartTime, &e.EndTime, &e.Attendees, &remindersRaw, &e.AllDay, &e.Timezone,
		&e.CategoryID, &e.RecurringEventID, &e.Visibility, &e.CreatedAt, &e.UpdatedAt)
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
		&e.CategoryID, &e.RecurringEventID, &e.Visibility, &e.CreatedAt, &e.UpdatedAt, &calOwnerID)
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
}

// calAccessFragJ is for queries that already JOIN calendars c ON c.id = e.calendar_id.
func calAccessFragJ(n int) string {
	p := "$" + itoa(n)
	return `(e.owner_id = ` + p + `
		OR c.owner_id = ` + p + `
		OR e.calendar_id IN (SELECT calendar_id FROM calendar_shares WHERE shared_with_user_id = ` + p + `))`
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

func (r *EventRepository) List(ctx context.Context, ownerID uuid.UUID, calendarID *uuid.UUID, from, to *time.Time) ([]model.Event, error) {
	query := `SELECT ` + eventColsJ + ` FROM events e JOIN calendars c ON c.id = e.calendar_id WHERE ` + calAccessFragJ(1)
	args := []any{ownerID}
	i := 2

	if calendarID != nil {
		query += ` AND e.calendar_id = $` + itoa(i)
		args = append(args, calendarID)
		i++
	}
	if from != nil {
		query += ` AND e.start_time >= $` + itoa(i)
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
	if req.CategoryID != nil {
		current.CategoryID = req.CategoryID
	}
	if req.Visibility != nil {
		current.Visibility = *req.Visibility
	}

	var e model.Event
	err = scanEvent(r.pool.QueryRow(ctx, `
		UPDATE events
		SET calendar_id=$1, title=$2, description=$3, location=$4,
		    start_time=$5, end_time=$6, attendees=$7, reminders=$8, all_day=$9, timezone=$10, category_id=$11, visibility=$12, updated_at=NOW()
		WHERE id=$13 AND owner_id=$14
		RETURNING `+eventCols,
		current.CalendarID, current.Title, current.Description, current.Location,
		current.StartTime, current.EndTime, current.Attendees, marshalReminders(current.Reminders),
		current.AllDay, current.Timezone, current.CategoryID, current.Visibility,
		id, ownerID), &e)
	return &e, err
}

func (r *EventRepository) Delete(ctx context.Context, id, ownerID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM events WHERE id = $1 AND owner_id = $2`, id, ownerID)
	return err
}

// DetachInstance clears recurring_event_id on an event, making it a standalone event.
func (r *EventRepository) DetachInstance(ctx context.Context, id, ownerID uuid.UUID) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE events SET recurring_event_id = NULL WHERE id = $1 AND owner_id = $2`, id, ownerID)
	return err
}

// Stats aggregates time spent over [from, to), grouped by Area (category) and
// then by sub-activity (event title) within each Area. Duration comes from the
// event's own span (end_time - start_time). All-day events are excluded — they
// carry no meaningful duration. Events with no category (NULL, or a since-deleted
// one) collect under a single "Uncategorized" entry.
func (r *EventRepository) Stats(ctx context.Context, ownerID uuid.UUID, from, to time.Time) (*model.TimeStats, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT e.category_id, COALESCE(c.name, ''), COALESCE(c.color, ''),
		       COALESCE(c.weekly_target_minutes, 0), e.title,
		       SUM(EXTRACT(EPOCH FROM (e.end_time - e.start_time)) / 60)::int
		FROM events e
		LEFT JOIN categories c ON c.id = e.category_id
		WHERE e.owner_id = $1 AND e.all_day = false
		  AND e.start_time >= $2 AND e.start_time < $3
		GROUP BY e.category_id, c.name, c.color, c.weekly_target_minutes, e.title
		ORDER BY c.name NULLS LAST, e.title`,
		ownerID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	stats := &model.TimeStats{From: from, To: to, Areas: []model.AreaStat{}}
	pos := map[string]int{} // group key -> index into stats.Areas
	for rows.Next() {
		var areaID *uuid.UUID
		var name, color, sub string
		var target, minutes int
		if err := rows.Scan(&areaID, &name, &color, &target, &sub, &minutes); err != nil {
			return nil, err
		}
		key := "none"
		if areaID != nil {
			key = areaID.String()
		}
		i, ok := pos[key]
		if !ok {
			if areaID == nil {
				name = "Uncategorized"
			}
			stats.Areas = append(stats.Areas, model.AreaStat{
				AreaID:              areaID,
				AreaName:            name,
				AreaColor:           color,
				WeeklyTargetMinutes: target,
				SubActivities:       []model.SubActivityStat{},
			})
			i = len(stats.Areas) - 1
			pos[key] = i
		}
		entry := &stats.Areas[i]
		entry.TotalMinutes += minutes
		if sub != "" {
			entry.SubActivities = append(entry.SubActivities, model.SubActivityStat{Name: sub, Minutes: minutes})
		}
	}
	return stats, rows.Err()
}

func itoa(i int) string {
	return strconv.Itoa(i)
}
