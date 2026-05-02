package repository

import (
	"context"

	"github.com/google/uuid"
	"github.com/hylin/calendar/api/internal/model"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CalendarShareRepository struct {
	pool *pgxpool.Pool
}

func NewCalendarShareRepository(pool *pgxpool.Pool) *CalendarShareRepository {
	return &CalendarShareRepository{pool: pool}
}

func (r *CalendarShareRepository) Create(ctx context.Context, calendarID, ownerID, sharedWithUserID uuid.UUID, permission string) (*model.CalendarShareDetail, error) {
	if ownerID == sharedWithUserID {
		return nil, ErrSelfShare
	}
	var d model.CalendarShareDetail
	err := r.pool.QueryRow(ctx, `
		WITH ins AS (
			INSERT INTO calendar_shares (calendar_id, owner_id, shared_with_user_id, permission)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (calendar_id, shared_with_user_id) DO UPDATE SET permission = EXCLUDED.permission
			RETURNING id, calendar_id, shared_with_user_id, permission, created_at
		)
		SELECT ins.id, ins.calendar_id, ins.shared_with_user_id, u.email, u.username, ins.permission, ins.created_at
		FROM ins JOIN users u ON u.id = ins.shared_with_user_id`,
		calendarID, ownerID, sharedWithUserID, permission,
	).Scan(&d.ID, &d.CalendarID, &d.SharedWithUserID, &d.SharedWithEmail, &d.SharedWithName, &d.Permission, &d.CreatedAt)
	return &d, err
}

func (r *CalendarShareRepository) List(ctx context.Context, calendarID, ownerID uuid.UUID) ([]model.CalendarShareDetail, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT cs.id, cs.calendar_id, cs.shared_with_user_id, u.email, u.username, cs.permission, cs.created_at
		FROM calendar_shares cs
		JOIN users u ON u.id = cs.shared_with_user_id
		WHERE cs.calendar_id = $1 AND cs.owner_id = $2
		ORDER BY cs.created_at ASC`,
		calendarID, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []model.CalendarShareDetail
	for rows.Next() {
		var d model.CalendarShareDetail
		if err := rows.Scan(&d.ID, &d.CalendarID, &d.SharedWithUserID, &d.SharedWithEmail, &d.SharedWithName, &d.Permission, &d.CreatedAt); err != nil {
			return nil, err
		}
		results = append(results, d)
	}
	if results == nil {
		results = []model.CalendarShareDetail{}
	}
	return results, rows.Err()
}

func (r *CalendarShareRepository) Delete(ctx context.Context, shareID, calendarID, ownerID uuid.UUID) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM calendar_shares WHERE id = $1 AND calendar_id = $2 AND owner_id = $3`,
		shareID, calendarID, ownerID)
	return err
}

// GetPermission returns the permission level for a user on a calendar they don't own,
// or pgx.ErrNoRows if no share exists.
func (r *CalendarShareRepository) GetPermission(ctx context.Context, calendarID, userID uuid.UUID) (string, error) {
	var permission string
	err := r.pool.QueryRow(ctx,
		`SELECT permission FROM calendar_shares WHERE calendar_id = $1 AND shared_with_user_id = $2`,
		calendarID, userID).Scan(&permission)
	return permission, err
}

func (r *CalendarShareRepository) ListSharedWithUser(ctx context.Context, userID uuid.UUID) ([]model.SharedCalendarEntry, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT c.id, c.owner_id, c.name, c.description, cs.permission, u.email, u.username
		FROM calendar_shares cs
		JOIN calendars c ON c.id = cs.calendar_id
		JOIN users u ON u.id = c.owner_id
		WHERE cs.shared_with_user_id = $1
		ORDER BY cs.created_at ASC`,
		userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []model.SharedCalendarEntry
	for rows.Next() {
		var e model.SharedCalendarEntry
		if err := rows.Scan(&e.ID, &e.OwnerID, &e.Name, &e.Description, &e.Permission, &e.OwnerEmail, &e.OwnerName); err != nil {
			return nil, err
		}
		results = append(results, e)
	}
	if results == nil {
		results = []model.SharedCalendarEntry{}
	}
	return results, rows.Err()
}
