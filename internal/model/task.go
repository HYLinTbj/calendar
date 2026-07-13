package model

import (
	"time"

	"github.com/google/uuid"
)

// Task is a lightweight backlog item under an Area. Deadlines are optional and
// there are no priorities or stages — just done / not done. CompletedAt is set
// automatically when Done flips to true.
type Task struct {
	ID          uuid.UUID  `json:"id"`
	OwnerID     uuid.UUID  `json:"owner_id"`
	AreaID      *uuid.UUID `json:"area_id,omitempty"`
	Title       string     `json:"title"`
	Notes       string     `json:"notes"`
	Done        bool       `json:"done"`
	DueDate     *time.Time `json:"due_date,omitempty"`
	Position    int        `json:"position"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

type CreateTaskRequest struct {
	AreaID   *uuid.UUID `json:"area_id"`
	Title    string     `json:"title" binding:"required"`
	Notes    string     `json:"notes"`
	DueDate  *time.Time `json:"due_date"` // RFC3339; date-only inputs should be sent as T00:00:00Z
	Position int        `json:"position"`
}

type UpdateTaskRequest struct {
	// AreaID and DueDate are Optional so an explicit null clears them; an
	// absent field keeps the current value.
	AreaID   Optional[uuid.UUID] `json:"area_id"`
	Title    *string             `json:"title"`
	Notes    *string             `json:"notes"`
	Done     *bool               `json:"done"`
	DueDate  Optional[time.Time] `json:"due_date"`
	Position *int                `json:"position"`
}
