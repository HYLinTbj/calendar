package worker

import (
	"context"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const windowDays = 60

type Worker struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Worker {
	return &Worker{pool: pool}
}

func (w *Worker) Run(ctx context.Context) {
	log.Println("scheduler worker started, running every hour")
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	w.process(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.process(ctx)
		}
	}
}

type recurringRule struct {
	id             uuid.UUID
	ownerID        uuid.UUID
	calendarID     uuid.UUID
	title          string
	description    string
	location       string
	durationNs     int64
	attendees      []string
	remindersJSON  []byte
	frequency      string
	interval       int
	daysOfWeek     []int
	endDate        *time.Time
	allDay         bool
	timezone       string
	categoryID     *uuid.UUID
	startTime      time.Time
	generatedUntil time.Time
}

func (w *Worker) process(ctx context.Context) {
	horizon := time.Now().UTC().Add(windowDays * 24 * time.Hour)

	rows, err := w.pool.Query(ctx, `
		SELECT id, owner_id, calendar_id, title, description, location, duration,
		       attendees, reminders, frequency, interval, days_of_week,
		       end_date, all_day, timezone, category_id, start_time, generated_until
		FROM recurring_events
		WHERE generated_until < $1`, horizon)
	if err != nil {
		log.Printf("query recurring_events: %v", err)
		return
	}
	defer rows.Close()

	var rules []recurringRule
	for rows.Next() {
		var r recurringRule
		if err := rows.Scan(
			&r.id, &r.ownerID, &r.calendarID, &r.title, &r.description, &r.location, &r.durationNs,
			&r.attendees, &r.remindersJSON, &r.frequency, &r.interval, &r.daysOfWeek,
			&r.endDate, &r.allDay, &r.timezone, &r.categoryID, &r.startTime, &r.generatedUntil,
		); err != nil {
			log.Printf("scan recurring rule: %v", err)
			return
		}
		rules = append(rules, r)
	}
	if err := rows.Err(); err != nil {
		log.Printf("iterate recurring_events: %v", err)
		return
	}

	for _, rule := range rules {
		if err := w.generateWindow(ctx, rule, horizon); err != nil {
			log.Printf("generate window for %s: %v", rule.id, err)
		}
	}
}

func (w *Worker) generateWindow(ctx context.Context, rule recurringRule, until time.Time) error {
	from := rule.generatedUntil
	occurrences := nextOccurrences(rule, from, until)
	duration := time.Duration(rule.durationNs)

	for _, start := range occurrences {
		end := start.Add(duration)
		_, err := w.pool.Exec(ctx, `
			INSERT INTO events
				(owner_id, calendar_id, title, description, location,
				 start_time, end_time, attendees, reminders, all_day, timezone, category_id, recurring_event_id)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
			ON CONFLICT (recurring_event_id, start_time) WHERE recurring_event_id IS NOT NULL DO NOTHING`,
			rule.ownerID, rule.calendarID, rule.title, rule.description, rule.location,
			start, end, rule.attendees, rule.remindersJSON, rule.allDay, rule.timezone, rule.categoryID, rule.id,
		)
		if err != nil {
			return err
		}
	}

	if len(rule.attendees) > 0 {
		_, err := w.pool.Exec(ctx, `
			INSERT INTO event_invitations (event_id, email)
			SELECT e.id, unnest($2::text[])
			FROM events e
			WHERE e.recurring_event_id = $1
			  AND e.start_time > $3
			  AND e.start_time <= $4
			ON CONFLICT (event_id, email) DO NOTHING`,
			rule.id, rule.attendees, from, until)
		if err != nil {
			return err
		}
	}

	_, err := w.pool.Exec(ctx,
		`UPDATE recurring_events SET generated_until=$1 WHERE id=$2`, until, rule.id)
	return err
}

func nextOccurrences(rule recurringRule, from, until time.Time) []time.Time {
	loc, err := time.LoadLocation(rule.timezone)
	if err != nil {
		loc = time.UTC
	}
	anchor := rule.startTime.In(loc)
	h, m, s, ns := anchor.Hour(), anchor.Minute(), anchor.Second(), anchor.Nanosecond()

	var results []time.Time
	current := rule.startTime
	for !current.After(from) {
		current = step(rule, current, loc, h, m, s, ns)
	}
	for !current.After(until) {
		if rule.endDate != nil && current.After(*rule.endDate) {
			break
		}
		results = append(results, current)
		current = step(rule, current, loc, h, m, s, ns)
	}
	return results
}

func step(rule recurringRule, t time.Time, loc *time.Location, h, m, s, ns int) time.Time {
	local := t.In(loc)
	n := rule.interval
	rebuild := func(d time.Time) time.Time {
		return time.Date(d.Year(), d.Month(), d.Day(), h, m, s, ns, loc).UTC()
	}
	switch rule.frequency {
	case "daily":
		return rebuild(local.AddDate(0, 0, n))
	case "weekly":
		if len(rule.daysOfWeek) == 0 {
			return rebuild(local.AddDate(0, 0, 7*n))
		}
		candidate := local.AddDate(0, 0, 1)
		limit := local.AddDate(0, 0, 7*n)
		for !candidate.After(limit) {
			for _, d := range rule.daysOfWeek {
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
