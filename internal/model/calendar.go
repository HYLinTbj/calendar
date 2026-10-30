package model

import (
	"time"

	"github.com/google/uuid"
)

// DefaultCalendarColor is a calendar's colour when none is given. It matches the
// column default in db.Migrate.
const DefaultCalendarColor = "#4f46e5"

type Calendar struct {
	ID          uuid.UUID `json:"id"`
	OwnerID     uuid.UUID `json:"owner_id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Color       string    `json:"color"`
	IsDefault   bool      `json:"is_default"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type CreateCalendarRequest struct {
	Name        string `json:"name"        binding:"required"`
	Description string `json:"description"`
	Color       string `json:"color"`
}

type UpdateCalendarRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Color       *string `json:"color"`
	IsDefault   *bool   `json:"is_default"`
}
