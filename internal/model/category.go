package model

import (
	"time"

	"github.com/google/uuid"
)

type Category struct {
	ID                  uuid.UUID  `json:"id"`
	OwnerID             uuid.UUID  `json:"owner_id"`
	GroupID             *uuid.UUID `json:"group_id,omitempty"`
	Name                string     `json:"name"`
	Code                string     `json:"code"`
	Color               string     `json:"color"`
	WeeklyTargetMinutes int        `json:"weekly_target_minutes"`
	Position            int        `json:"position"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

type CreateCategoryRequest struct {
	Name                string     `json:"name"  binding:"required"`
	Color               string     `json:"color" binding:"required"`
	WeeklyTargetMinutes int        `json:"weekly_target_minutes"`
	GroupID             *uuid.UUID `json:"group_id"`
	// Code is optional; when empty the server derives a unique one from Name.
	Code     string `json:"code"`
	Position int    `json:"position"`
}

type UpdateCategoryRequest struct {
	Name                *string `json:"name"`
	Color               *string `json:"color"`
	WeeklyTargetMinutes *int    `json:"weekly_target_minutes"`
	// GroupID is Optional so an explicit null ungroups the Area; an absent
	// field keeps the current group.
	GroupID  Optional[uuid.UUID] `json:"group_id"`
	Code     *string             `json:"code"`
	Position *int                `json:"position"`
}
