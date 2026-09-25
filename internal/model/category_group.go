package model

import (
	"time"

	"github.com/google/uuid"
)

// CategoryGroup is an optional parent level over Areas (categories), e.g.
// "Language learning" holding French and Japanese. It carries the colour its
// Areas share in the UI.
type CategoryGroup struct {
	ID        uuid.UUID `json:"id"`
	OwnerID   uuid.UUID `json:"owner_id"`
	Name      string    `json:"name"`
	Color     string    `json:"color"`
	Position  int       `json:"position"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type CreateCategoryGroupRequest struct {
	Name     string `json:"name"  binding:"required"`
	Color    string `json:"color" binding:"required"`
	Position int    `json:"position"`
}

type UpdateCategoryGroupRequest struct {
	Name     *string `json:"name"`
	Color    *string `json:"color"`
	Position *int    `json:"position"`
}
