package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
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
