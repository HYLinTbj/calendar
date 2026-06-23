package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/hylin/calendar/internal/model"
	"github.com/jackc/pgx/v5/pgxpool"
)

type TaskRepository struct {
	pool *pgxpool.Pool
}

func NewTaskRepository(pool *pgxpool.Pool) *TaskRepository {
	return &TaskRepository{pool: pool}
}

const taskCols = `id, owner_id, area_id, title, notes, done, due_date, position, completed_at, created_at, updated_at`

func scanTask(row interface{ Scan(...any) error }, t *model.Task) error {
	return row.Scan(&t.ID, &t.OwnerID, &t.AreaID, &t.Title, &t.Notes, &t.Done,
		&t.DueDate, &t.Position, &t.CompletedAt, &t.CreatedAt, &t.UpdatedAt)
}

func (r *TaskRepository) Create(ctx context.Context, ownerID uuid.UUID, req model.CreateTaskRequest) (*model.Task, error) {
	var t model.Task
	err := scanTask(r.pool.QueryRow(ctx, `
		INSERT INTO tasks (owner_id, area_id, title, notes, due_date, position)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+taskCols,
		ownerID, req.AreaID, req.Title, req.Notes, req.DueDate, req.Position), &t)
	return &t, err
}

func (r *TaskRepository) GetByID(ctx context.Context, id, ownerID uuid.UUID) (*model.Task, error) {
	var t model.Task
	err := scanTask(r.pool.QueryRow(ctx,
		`SELECT `+taskCols+` FROM tasks WHERE id=$1 AND owner_id=$2`, id, ownerID), &t)
	return &t, err
}

func (r *TaskRepository) List(ctx context.Context, ownerID uuid.UUID, areaID *uuid.UUID, done *bool) ([]model.Task, error) {
	query := `SELECT ` + taskCols + ` FROM tasks WHERE owner_id=$1`
	args := []any{ownerID}
	i := 2
	if areaID != nil {
		query += ` AND area_id = $` + itoa(i)
		args = append(args, areaID)
		i++
	}
	if done != nil {
		query += ` AND done = $` + itoa(i)
		args = append(args, done)
		i++
	}
	// Open tasks first, then by manual position, then oldest-first.
	query += ` ORDER BY done ASC, position ASC, created_at ASC`

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tasks := []model.Task{}
	for rows.Next() {
		var t model.Task
		if err := scanTask(rows, &t); err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

func (r *TaskRepository) Update(ctx context.Context, id, ownerID uuid.UUID, req model.UpdateTaskRequest) (*model.Task, error) {
	current, err := r.GetByID(ctx, id, ownerID)
	if err != nil {
		return nil, err
	}
	if req.AreaID != nil {
		current.AreaID = req.AreaID
	}
	if req.Title != nil {
		current.Title = *req.Title
	}
	if req.Notes != nil {
		current.Notes = *req.Notes
	}
	if req.DueDate != nil {
		current.DueDate = req.DueDate
	}
	if req.Position != nil {
		current.Position = *req.Position
	}
	// Toggle completed_at on the done transition so it always reflects reality.
	if req.Done != nil && *req.Done != current.Done {
		current.Done = *req.Done
		if current.Done {
			now := time.Now()
			current.CompletedAt = &now
		} else {
			current.CompletedAt = nil
		}
	}
	var t model.Task
	err = scanTask(r.pool.QueryRow(ctx, `
		UPDATE tasks
		SET area_id=$1, title=$2, notes=$3, done=$4, due_date=$5, position=$6, completed_at=$7, updated_at=NOW()
		WHERE id=$8 AND owner_id=$9
		RETURNING `+taskCols,
		current.AreaID, current.Title, current.Notes, current.Done, current.DueDate,
		current.Position, current.CompletedAt, id, ownerID), &t)
	return &t, err
}

func (r *TaskRepository) Delete(ctx context.Context, id, ownerID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM tasks WHERE id=$1 AND owner_id=$2`, id, ownerID)
	return err
}
