package repository

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/hylin/calendar/internal/model"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CalendarRepository struct {
	pool *pgxpool.Pool
}

func NewCalendarRepository(pool *pgxpool.Pool) *CalendarRepository {
	return &CalendarRepository{pool: pool}
}

const calendarCols = `id, owner_id, name, description, color, is_default, created_at, updated_at`

// calendarColsC is calendarCols for queries that alias calendars as c.
const calendarColsC = `c.id, c.owner_id, c.name, c.description, c.color, c.is_default, c.created_at, c.updated_at`

// scanCalendar scans calendarCols, then extra.
func scanCalendar(row interface{ Scan(...any) error }, c *model.Calendar, extra ...any) error {
	return row.Scan(append([]any{&c.ID, &c.OwnerID, &c.Name, &c.Description, &c.Color, &c.IsDefault, &c.CreatedAt, &c.UpdatedAt}, extra...)...)
}

// Create adds a calendar. A blank color gets model.DefaultCalendarColor.
func (r *CalendarRepository) Create(ctx context.Context, ownerID uuid.UUID, req model.CreateCalendarRequest) (*model.Calendar, error) {
	if strings.TrimSpace(req.Color) == "" {
		req.Color = model.DefaultCalendarColor
	}
	var c model.Calendar
	err := scanCalendar(r.pool.QueryRow(ctx, `
		INSERT INTO calendars (owner_id, name, description, color)
		VALUES ($1, $2, $3, $4)
		RETURNING `+calendarCols,
		ownerID, req.Name, req.Description, req.Color), &c)
	return &c, err
}

func (r *CalendarRepository) CreateDefault(ctx context.Context, ownerID uuid.UUID) (*model.Calendar, error) {
	var c model.Calendar
	err := scanCalendar(r.pool.QueryRow(ctx, `
		INSERT INTO calendars (owner_id, name, color, is_default)
		VALUES ($1, 'My Calendar', $2, true)
		RETURNING `+calendarCols,
		ownerID, model.DefaultCalendarColor), &c)
	return &c, err
}

func (r *CalendarRepository) GetByID(ctx context.Context, id, ownerID uuid.UUID) (*model.Calendar, error) {
	var c model.Calendar
	err := scanCalendar(r.pool.QueryRow(ctx,
		`SELECT `+calendarCols+` FROM calendars WHERE id = $1 AND owner_id = $2`, id, ownerID), &c)
	return &c, err
}

// GetByIDOrShared returns a calendar if userID is the owner or has a share.
// The second return value is "owner", "edit", or "view".
func (r *CalendarRepository) GetByIDOrShared(ctx context.Context, id, userID uuid.UUID) (*model.Calendar, string, error) {
	cal, err := r.GetByID(ctx, id, userID)
	if err == nil {
		return cal, "owner", nil
	}
	// Not the owner; check for a share
	var c model.Calendar
	var permission string
	err = scanCalendar(r.pool.QueryRow(ctx, `
		SELECT `+calendarColsC+`, cs.permission
		FROM calendars c
		JOIN calendar_shares cs ON cs.calendar_id = c.id
		WHERE c.id = $1 AND cs.shared_with_user_id = $2`,
		id, userID,
	), &c, &permission)
	if err != nil {
		return nil, "", err
	}
	return &c, permission, nil
}

func (r *CalendarRepository) GetDefault(ctx context.Context, ownerID uuid.UUID) (*model.Calendar, error) {
	var c model.Calendar
	err := scanCalendar(r.pool.QueryRow(ctx,
		`SELECT `+calendarCols+` FROM calendars WHERE owner_id = $1 AND is_default = true`, ownerID), &c)
	return &c, err
}

func (r *CalendarRepository) List(ctx context.Context, ownerID uuid.UUID) ([]model.Calendar, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+calendarCols+`
		FROM calendars WHERE owner_id = $1
		ORDER BY is_default DESC, created_at ASC
	`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	calendars := []model.Calendar{}
	for rows.Next() {
		var c model.Calendar
		if err := scanCalendar(rows, &c); err != nil {
			return nil, err
		}
		calendars = append(calendars, c)
	}
	return calendars, rows.Err()
}

func (r *CalendarRepository) Update(ctx context.Context, id, ownerID uuid.UUID, req model.UpdateCalendarRequest) (*model.Calendar, error) {
	current, err := r.GetByID(ctx, id, ownerID)
	if err != nil {
		return nil, err
	}
	if req.Name != nil {
		current.Name = *req.Name
	}
	if req.Description != nil {
		current.Description = *req.Description
	}
	if req.Color != nil {
		current.Color = *req.Color
	}

	// Switching the default requires unsetting the old one first (within a transaction).
	if req.IsDefault != nil && *req.IsDefault && !current.IsDefault {
		tx, err := r.pool.Begin(ctx)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback(ctx)

		if _, err := tx.Exec(ctx,
			`UPDATE calendars SET is_default = false, updated_at = NOW() WHERE owner_id = $1 AND is_default = true`,
			ownerID,
		); err != nil {
			return nil, err
		}
		var c model.Calendar
		if err := scanCalendar(tx.QueryRow(ctx, `
			UPDATE calendars SET name=$1, description=$2, color=$3, is_default=true, updated_at=NOW()
			WHERE id=$4 AND owner_id=$5
			RETURNING `+calendarCols,
			current.Name, current.Description, current.Color, id, ownerID), &c); err != nil {
			return nil, err
		}
		return &c, tx.Commit(ctx)
	}

	var c model.Calendar
	err = scanCalendar(r.pool.QueryRow(ctx, `
		UPDATE calendars SET name=$1, description=$2, color=$3, updated_at=NOW()
		WHERE id=$4 AND owner_id=$5
		RETURNING `+calendarCols,
		current.Name, current.Description, current.Color, id, ownerID), &c)
	return &c, err
}

// Delete moves the calendar's events and recurring series to the user's default
// calendar, then deletes it.
// Returns ErrDeleteDefault if called on the default calendar, including one that
// became the default after the check below (a concurrent switch).
func (r *CalendarRepository) Delete(ctx context.Context, id, ownerID uuid.UUID) error {
	cal, err := r.GetByID(ctx, id, ownerID)
	if err != nil {
		return err
	}
	if cal.IsDefault {
		return ErrDeleteDefault
	}

	def, err := r.GetDefault(ctx, ownerID)
	if err != nil {
		return err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`UPDATE events SET calendar_id = $1 WHERE calendar_id = $2`,
		def.ID, id,
	); err != nil {
		return err
	}
	// Series too: recurring_events.calendar_id is ON DELETE RESTRICT, so one left behind
	// would make the delete fail.
	if _, err := tx.Exec(ctx,
		`UPDATE recurring_events SET calendar_id = $1 WHERE calendar_id = $2`,
		def.ID, id,
	); err != nil {
		return err
	}
	// The check above ran outside this transaction, so the DELETE checks again: a row
	// a concurrent switch made the default is re-read here (once that switch commits)
	// and kept, and the moves roll back. Deleting it would leave the user no default.
	tag, err := tx.Exec(ctx, `DELETE FROM calendars WHERE id = $1 AND owner_id = $2 AND NOT is_default`, id, ownerID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrDeleteDefault
	}
	return tx.Commit(ctx)
}
