package repository

import (
	"context"

	"github.com/google/uuid"
	"github.com/hylin/calendar/internal/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type TraceRepository struct {
	pool *pgxpool.Pool
}

func NewTraceRepository(pool *pgxpool.Pool) *TraceRepository {
	return &TraceRepository{pool: pool}
}

// day is read back as text so it stays a plain "YYYY-MM-DD" (model.Date).
const traceCols = `id, owner_id, category_id, to_char(day, 'YYYY-MM-DD'), minutes, note, created_at, updated_at`

func scanTrace(row interface{ Scan(...any) error }, t *model.Trace) error {
	var day string
	if err := row.Scan(&t.ID, &t.OwnerID, &t.CategoryID, &day, &t.Minutes, &t.Note, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return err
	}
	t.Day = model.Date(day)
	return nil
}

func (r *TraceRepository) Create(ctx context.Context, ownerID uuid.UUID, req model.CreateTraceRequest) (*model.Trace, error) {
	var t model.Trace
	err := scanTrace(r.pool.QueryRow(ctx, `
		INSERT INTO time_traces (owner_id, category_id, day, minutes, note)
		VALUES ($1, $2, $3::date, $4, $5)
		RETURNING `+traceCols,
		ownerID, req.CategoryID, string(req.Day), req.Minutes, req.Note), &t)
	return &t, err
}

func (r *TraceRepository) GetByID(ctx context.Context, id, ownerID uuid.UUID) (*model.Trace, error) {
	var t model.Trace
	err := scanTrace(r.pool.QueryRow(ctx,
		`SELECT `+traceCols+` FROM time_traces WHERE id=$1 AND owner_id=$2`, id, ownerID), &t)
	return &t, err
}

// List returns the owner's traces from fromDay through toDay (both inclusive),
// newest day first and, within a day, most recently added first.
func (r *TraceRepository) List(ctx context.Context, ownerID uuid.UUID, fromDay, toDay model.Date) ([]model.Trace, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+traceCols+` FROM time_traces
		WHERE owner_id=$1 AND day >= $2::date AND day <= $3::date
		ORDER BY day DESC, created_at DESC`,
		ownerID, string(fromDay), string(toDay))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	traces := []model.Trace{}
	for rows.Next() {
		var t model.Trace
		if err := scanTrace(rows, &t); err != nil {
			return nil, err
		}
		traces = append(traces, t)
	}
	return traces, rows.Err()
}

func (r *TraceRepository) Update(ctx context.Context, id, ownerID uuid.UUID, req model.UpdateTraceRequest) (*model.Trace, error) {
	current, err := r.GetByID(ctx, id, ownerID)
	if err != nil {
		return nil, err
	}
	if req.CategoryID.Set {
		current.CategoryID = req.CategoryID.Value
	}
	if req.Day != nil {
		current.Day = *req.Day
	}
	if req.Minutes != nil {
		current.Minutes = *req.Minutes
	}
	if req.Note != nil {
		current.Note = *req.Note
	}
	var t model.Trace
	err = scanTrace(r.pool.QueryRow(ctx, `
		UPDATE time_traces
		SET category_id=$1, day=$2::date, minutes=$3, note=$4, updated_at=NOW()
		WHERE id=$5 AND owner_id=$6
		RETURNING `+traceCols,
		current.CategoryID, string(current.Day), current.Minutes, current.Note, id, ownerID), &t)
	return &t, err
}

// Delete removes one of the owner's traces; pgx.ErrNoRows if there is none.
func (r *TraceRepository) Delete(ctx context.Context, id, ownerID uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM time_traces WHERE id=$1 AND owner_id=$2`, id, ownerID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}
