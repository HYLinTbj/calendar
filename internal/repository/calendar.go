package repository

import (
	"context"

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

func (r *CalendarRepository) Create(ctx context.Context, ownerID uuid.UUID, name, description string) (*model.Calendar, error) {
	var c model.Calendar
	err := r.pool.QueryRow(ctx, `
		INSERT INTO calendars (owner_id, name, description)
		VALUES ($1, $2, $3)
		RETURNING id, owner_id, name, description, is_default, created_at, updated_at
	`, ownerID, name, description).
		Scan(&c.ID, &c.OwnerID, &c.Name, &c.Description, &c.IsDefault, &c.CreatedAt, &c.UpdatedAt)
	return &c, err
}

func (r *CalendarRepository) CreateDefault(ctx context.Context, ownerID uuid.UUID) (*model.Calendar, error) {
	var c model.Calendar
	err := r.pool.QueryRow(ctx, `
		INSERT INTO calendars (owner_id, name, is_default)
		VALUES ($1, 'My Calendar', true)
		RETURNING id, owner_id, name, description, is_default, created_at, updated_at
	`, ownerID).
		Scan(&c.ID, &c.OwnerID, &c.Name, &c.Description, &c.IsDefault, &c.CreatedAt, &c.UpdatedAt)
	return &c, err
}

func (r *CalendarRepository) GetByID(ctx context.Context, id, ownerID uuid.UUID) (*model.Calendar, error) {
	var c model.Calendar
	err := r.pool.QueryRow(ctx, `
		SELECT id, owner_id, name, description, is_default, created_at, updated_at
		FROM calendars WHERE id = $1 AND owner_id = $2
	`, id, ownerID).
		Scan(&c.ID, &c.OwnerID, &c.Name, &c.Description, &c.IsDefault, &c.CreatedAt, &c.UpdatedAt)
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
	err = r.pool.QueryRow(ctx, `
		SELECT c.id, c.owner_id, c.name, c.description, c.is_default, c.created_at, c.updated_at, cs.permission
		FROM calendars c
		JOIN calendar_shares cs ON cs.calendar_id = c.id
		WHERE c.id = $1 AND cs.shared_with_user_id = $2`,
		id, userID,
	).Scan(&c.ID, &c.OwnerID, &c.Name, &c.Description, &c.IsDefault, &c.CreatedAt, &c.UpdatedAt, &permission)
	if err != nil {
		return nil, "", err
	}
	return &c, permission, nil
}

func (r *CalendarRepository) GetDefault(ctx context.Context, ownerID uuid.UUID) (*model.Calendar, error) {
	var c model.Calendar
	err := r.pool.QueryRow(ctx, `
		SELECT id, owner_id, name, description, is_default, created_at, updated_at
		FROM calendars WHERE owner_id = $1 AND is_default = true
	`, ownerID).
		Scan(&c.ID, &c.OwnerID, &c.Name, &c.Description, &c.IsDefault, &c.CreatedAt, &c.UpdatedAt)
	return &c, err
}

func (r *CalendarRepository) List(ctx context.Context, ownerID uuid.UUID) ([]model.Calendar, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, owner_id, name, description, is_default, created_at, updated_at
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
		if err := rows.Scan(&c.ID, &c.OwnerID, &c.Name, &c.Description, &c.IsDefault, &c.CreatedAt, &c.UpdatedAt); err != nil {
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
		if err := tx.QueryRow(ctx, `
			UPDATE calendars SET name=$1, description=$2, is_default=true, updated_at=NOW()
			WHERE id=$3 AND owner_id=$4
			RETURNING id, owner_id, name, description, is_default, created_at, updated_at
		`, current.Name, current.Description, id, ownerID).
			Scan(&c.ID, &c.OwnerID, &c.Name, &c.Description, &c.IsDefault, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		return &c, tx.Commit(ctx)
	}

	var c model.Calendar
	err = r.pool.QueryRow(ctx, `
		UPDATE calendars SET name=$1, description=$2, updated_at=NOW()
		WHERE id=$3 AND owner_id=$4
		RETURNING id, owner_id, name, description, is_default, created_at, updated_at
	`, current.Name, current.Description, id, ownerID).
		Scan(&c.ID, &c.OwnerID, &c.Name, &c.Description, &c.IsDefault, &c.CreatedAt, &c.UpdatedAt)
	return &c, err
}

// Delete moves the calendar's events to the user's default calendar, then deletes it.
// Returns an error if called on the default calendar.
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
	if _, err := tx.Exec(ctx, `DELETE FROM calendars WHERE id = $1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
