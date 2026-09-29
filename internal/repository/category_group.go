package repository

import (
	"context"

	"github.com/google/uuid"
	"github.com/hylin/calendar/internal/model"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CategoryGroupRepository struct {
	pool *pgxpool.Pool
}

func NewCategoryGroupRepository(pool *pgxpool.Pool) *CategoryGroupRepository {
	return &CategoryGroupRepository{pool: pool}
}

const categoryGroupCols = `id, owner_id, name, color, position, created_at, updated_at`

func scanCategoryGroup(row interface{ Scan(...any) error }, g *model.CategoryGroup) error {
	return row.Scan(&g.ID, &g.OwnerID, &g.Name, &g.Color, &g.Position, &g.CreatedAt, &g.UpdatedAt)
}

func (r *CategoryGroupRepository) Create(ctx context.Context, ownerID uuid.UUID, req model.CreateCategoryGroupRequest) (*model.CategoryGroup, error) {
	var g model.CategoryGroup
	err := scanCategoryGroup(r.pool.QueryRow(ctx, `
		INSERT INTO category_groups (owner_id, name, color, position)
		VALUES ($1, $2, $3, $4)
		RETURNING `+categoryGroupCols,
		ownerID, req.Name, req.Color, req.Position), &g)
	return &g, err
}

func (r *CategoryGroupRepository) GetByID(ctx context.Context, id, ownerID uuid.UUID) (*model.CategoryGroup, error) {
	var g model.CategoryGroup
	err := scanCategoryGroup(r.pool.QueryRow(ctx,
		`SELECT `+categoryGroupCols+` FROM category_groups WHERE id=$1 AND owner_id=$2`, id, ownerID), &g)
	return &g, err
}

func (r *CategoryGroupRepository) List(ctx context.Context, ownerID uuid.UUID) ([]model.CategoryGroup, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+categoryGroupCols+` FROM category_groups WHERE owner_id=$1 ORDER BY position ASC, name ASC`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	results := []model.CategoryGroup{}
	for rows.Next() {
		var g model.CategoryGroup
		if err := scanCategoryGroup(rows, &g); err != nil {
			return nil, err
		}
		results = append(results, g)
	}
	return results, rows.Err()
}

func (r *CategoryGroupRepository) Update(ctx context.Context, id, ownerID uuid.UUID, req model.UpdateCategoryGroupRequest) (*model.CategoryGroup, error) {
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
	if req.Position != nil {
		current.Position = *req.Position
	}
	var g model.CategoryGroup
	err = scanCategoryGroup(r.pool.QueryRow(ctx, `
		UPDATE category_groups SET name=$1, color=$2, position=$3, updated_at=NOW()
		WHERE id=$4 AND owner_id=$5
		RETURNING `+categoryGroupCols,
		current.Name, current.Color, current.Position, id, ownerID), &g)
	return &g, err
}

// Delete removes the group; its Areas survive ungrouped (group_id ON DELETE SET NULL).
func (r *CategoryGroupRepository) Delete(ctx context.Context, id, ownerID uuid.UUID) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM category_groups WHERE id=$1 AND owner_id=$2`, id, ownerID)
	return err
}
