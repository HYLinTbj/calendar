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

const categoryCols = `id, owner_id, group_id, name, code, color, weekly_target_minutes, position, created_at, updated_at`

func scanCategory(row interface{ Scan(...any) error }, c *model.Category) error {
	return row.Scan(&c.ID, &c.OwnerID, &c.GroupID, &c.Name, &c.Code, &c.Color, &c.WeeklyTargetMinutes, &c.Position, &c.CreatedAt, &c.UpdatedAt)
}

// Create inserts an Area. An empty req.Code is replaced by one derived from the
// name that no other Area of the owner uses; a non-empty code is expected to be
// normalized already (model.NormalizeCategoryCode).
func (r *CategoryRepository) Create(ctx context.Context, ownerID uuid.UUID, req model.CreateCategoryRequest) (*model.Category, error) {
	code := req.Code
	if code == "" {
		used, err := r.usedCodes(ctx, ownerID)
		if err != nil {
			return nil, err
		}
		code = model.DeriveCategoryCode(req.Name, used)
	}
	var c model.Category
	err := scanCategory(r.pool.QueryRow(ctx, `
		INSERT INTO categories (owner_id, group_id, name, code, color, weekly_target_minutes, position)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING `+categoryCols,
		ownerID, req.GroupID, req.Name, code, req.Color, req.WeeklyTargetMinutes, req.Position), &c)
	return &c, err
}

func (r *CategoryRepository) usedCodes(ctx context.Context, ownerID uuid.UUID) (map[string]bool, error) {
	rows, err := r.pool.Query(ctx, `SELECT code FROM categories WHERE owner_id=$1`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	used := map[string]bool{}
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return nil, err
		}
		used[code] = true
	}
	return used, rows.Err()
}

func (r *CategoryRepository) GetByID(ctx context.Context, id, ownerID uuid.UUID) (*model.Category, error) {
	var c model.Category
	err := scanCategory(r.pool.QueryRow(ctx,
		`SELECT `+categoryCols+` FROM categories WHERE id=$1 AND owner_id=$2`, id, ownerID), &c)
	return &c, err
}

func (r *CategoryRepository) List(ctx context.Context, ownerID uuid.UUID) ([]model.Category, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+categoryCols+` FROM categories WHERE owner_id=$1 ORDER BY position ASC, name ASC`, ownerID)
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
	if req.GroupID.Set {
		current.GroupID = req.GroupID.Value
	}
	if req.Code != nil {
		current.Code = *req.Code
	}
	if req.Position != nil {
		current.Position = *req.Position
	}
	var c model.Category
	err = scanCategory(r.pool.QueryRow(ctx, `
		UPDATE categories SET name=$1, color=$2, weekly_target_minutes=$3, group_id=$4, code=$5, position=$6, updated_at=NOW()
		WHERE id=$7 AND owner_id=$8
		RETURNING `+categoryCols,
		current.Name, current.Color, current.WeeklyTargetMinutes, current.GroupID, current.Code, current.Position, id, ownerID), &c)
	return &c, err
}

func (r *CategoryRepository) Delete(ctx context.Context, id, ownerID uuid.UUID) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM categories WHERE id=$1 AND owner_id=$2`, id, ownerID)
	return err
}
