package model

import (
	"time"

	"github.com/google/uuid"
)

type Category struct {
	ID        uuid.UUID `json:"id"`
	OwnerID   uuid.UUID `json:"owner_id"`
	Name      string    `json:"name"`
	Color     string    `json:"color"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type CreateCategoryRequest struct {
	Name  string `json:"name"  binding:"required"`
	Color string `json:"color" binding:"required"`
}

type UpdateCategoryRequest struct {
	Name  *string `json:"name"`
	Color *string `json:"color"`
}
