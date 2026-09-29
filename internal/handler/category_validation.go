package handler

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hylin/calendar/internal/model"
	"github.com/hylin/calendar/internal/repository"
	"github.com/jackc/pgx/v5"
)

// validateCategoryOwnership checks that a requested category (a.k.a. Area) exists
// and belongs to the caller, so writes can't reference a bogus id (which would hit
// the FK constraint as a 500) or another tenant's category (which would leak that
// category's name/colour across tenants). A nil id — "no category" or an explicit
// clear — is always valid. On failure it writes the error response and returns
// false; notFoundMsg names the field for the caller's vocabulary ("category" vs
// "area").
func validateCategoryOwnership(c *gin.Context, catRepo *repository.CategoryRepository, ownerID uuid.UUID, categoryID *uuid.UUID, notFoundMsg string) bool {
	if categoryID == nil {
		return true
	}
	_, err := catRepo.GetByID(c.Request.Context(), *categoryID, ownerID)
	if err == pgx.ErrNoRows {
		c.JSON(http.StatusBadRequest, gin.H{"error": notFoundMsg})
		return false
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return false
	}
	return true
}

// validateGroupOwnership is validateCategoryOwnership for an Area's group_id:
// the FK alone would accept another tenant's group.
func validateGroupOwnership(c *gin.Context, groupRepo *repository.CategoryGroupRepository, ownerID uuid.UUID, groupID *uuid.UUID) bool {
	if groupID == nil {
		return true
	}
	_, err := groupRepo.GetByID(c.Request.Context(), *groupID, ownerID)
	if err == pgx.ErrNoRows {
		c.JSON(http.StatusBadRequest, gin.H{"error": "group not found"})
		return false
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return false
	}
	return true
}

// normalizeCodeField validates an optional caller-supplied Area code in place.
// A blank code is left empty, which means "derive one from the name" (on create
// and update alike). On failure it writes a 400 and returns false.
func normalizeCodeField(c *gin.Context, code *string) bool {
	if code == nil {
		return true
	}
	if strings.TrimSpace(*code) == "" {
		*code = ""
		return true
	}
	norm, ok := model.NormalizeCategoryCode(*code)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "code must be 1-4 letters or digits"})
		return false
	}
	*code = norm
	return true
}

// rejectBlank writes a 400 and returns false if name or color (both required on
// create) is empty or whitespace. A nil one, absent from an update, passes.
func rejectBlank(c *gin.Context, name, color *string) bool {
	for field, v := range map[string]*string{"name": name, "color": color} {
		if v != nil && strings.TrimSpace(*v) == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": field + " must not be blank"})
			return false
		}
	}
	return true
}

// categoryConflictMessage names which per-owner uniqueness rule a 23505 hit.
func categoryConflictMessage(err error) string {
	if repository.IsCategoryCodeConflict(err) {
		return "category code already exists"
	}
	return "category name already exists"
}
