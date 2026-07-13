package model

import (
	"time"

	"github.com/google/uuid"
)

type CalendarShareDetail struct {
	ID               uuid.UUID `json:"id"`
	CalendarID       uuid.UUID `json:"calendar_id"`
	SharedWithUserID uuid.UUID `json:"shared_with_user_id"`
	SharedWithEmail  string    `json:"shared_with_email"`
	SharedWithName   string    `json:"shared_with_name"`
	Permission       string    `json:"permission"`
	CreatedAt        time.Time `json:"created_at"`
}

type SharedCalendarEntry struct {
	ID          uuid.UUID `json:"id"`
	OwnerID     uuid.UUID `json:"owner_id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Permission  string    `json:"permission"`
	OwnerEmail  string    `json:"owner_email"`
	OwnerName   string    `json:"owner_name"`
}

type CreateShareRequest struct {
	Email      string `json:"email"      binding:"required,email"`
	Permission string `json:"permission" binding:"required,oneof=view edit"`
}
