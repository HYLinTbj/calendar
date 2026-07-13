package repository

import (
	"context"

	"github.com/google/uuid"
	"github.com/hylin/calendar/internal/model"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CategoryRepository struct {
	pool *pgxpool.Pool
}

func NewCategoryRepository(pool *pgxpool.Pool) *CategoryRepository {
	return &CategoryRepository{pool: pool}
}

const categoryCols = `id, owner_id, name, color, weekly_target_minutes, created_at, updated_at`

func scanCategory(row interface{ Scan(...any) error }, c *model.Category) error {
	return row.Scan(&c.ID, &c.OwnerID, &c.Name, &c.Color, &c.WeeklyTargetMinutes, &c.CreatedAt, &c.UpdatedAt)
}

func (r *CategoryRepository) Create(ctx context.Context, ownerID uuid.UUID, req model.CreateCategoryRequest) (*model.Category, error) {
	var c model.Category
	err := scanCategory(r.pool.QueryRow(ctx, `
		INSERT INTO categories (owner_id, name, color, weekly_target_minutes)
		VALUES ($1, $2, $3, $4)
		RETURNING `+categoryCols,
		ownerID, req.Name, req.Color, req.WeeklyTargetMinutes), &c)
	return &c, err
}

func (r *CategoryRepository) GetByID(ctx context.Context, id, ownerID uuid.UUID) (*model.Category, error) {
	var c model.Category
	err := scanCategory(r.pool.QueryRow(ctx,
		`SELECT `+categoryCols+` FROM categories WHERE id=$1 AND owner_id=$2`, id, ownerID), &c)
	return &c, err
}

func (r *CategoryRepository) List(ctx context.Context, ownerID uuid.UUID) ([]model.Category, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+categoryCols+` FROM categories WHERE owner_id=$1 ORDER BY name ASC`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []model.Category
	for rows.Next() {
		var c model.Category
		if err := scanCategory(rows, &c); err != nil {
			return nil, err
		}
		results = append(results, c)
	}
	if results == nil {
		results = []model.Category{}
	}
	return results, rows.Err()
}

func (r *CategoryRepository) Update(ctx context.Context, id, ownerID uuid.UUID, req model.UpdateCategoryRequest) (*model.Category, error) {
	current, err := r.GetByID(ctx, id, ownerID)
	if err != nil {
		return nil, err
	}
	if req.Name != nil {
		current.Name = *req.Name
	}
	if req.Color != nil {
		current.Color = *req.Color
	}
	if req.WeeklyTargetMinutes != nil {
		current.WeeklyTargetMinutes = *req.WeeklyTargetMinutes
	}
	var c model.Category
	err = scanCategory(r.pool.QueryRow(ctx, `
		UPDATE categories SET name=$1, color=$2, weekly_target_minutes=$3, updated_at=NOW()
		WHERE id=$4 AND owner_id=$5
		RETURNING `+categoryCols,
		current.Name, current.Color, current.WeeklyTargetMinutes, id, ownerID), &c)
	return &c, err
}

func (r *CategoryRepository) Delete(ctx context.Context, id, ownerID uuid.UUID) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM categories WHERE id=$1 AND owner_id=$2`, id, ownerID)
	return err
}
