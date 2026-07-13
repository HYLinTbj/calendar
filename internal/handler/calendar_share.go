package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hylin/calendar/internal/middleware"
	"github.com/hylin/calendar/internal/model"
	"github.com/hylin/calendar/internal/repository"
	"github.com/jackc/pgx/v5"
)

type CalendarShareHandler struct {
	shareRepo *repository.CalendarShareRepository
	calRepo   *repository.CalendarRepository
	userRepo  *repository.UserRepository
}

func NewCalendarShareHandler(shareRepo *repository.CalendarShareRepository, calRepo *repository.CalendarRepository, userRepo *repository.UserRepository) *CalendarShareHandler {
	return &CalendarShareHandler{shareRepo: shareRepo, calRepo: calRepo, userRepo: userRepo}
}

func (h *CalendarShareHandler) Share(c *gin.Context) {
	ownerID := c.MustGet(middleware.UserIDKey).(uuid.UUID)
	calID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	// Verify the calendar belongs to the requester.
	if _, err := h.calRepo.GetByID(c.Request.Context(), calID, ownerID); err == pgx.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var req model.CreateShareRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	target, _, err := h.userRepo.GetByEmail(c.Request.Context(), req.Email)
	if err == pgx.ErrNoRows {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "user not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	share, err := h.shareRepo.Create(c.Request.Context(), calID, ownerID, target.ID, req.Permission)
	if errors.Is(err, repository.ErrSelfShare) {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, share)
}

func (h *CalendarShareHandler) ListShares(c *gin.Context) {
	ownerID := c.MustGet(middleware.UserIDKey).(uuid.UUID)
	calID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	if _, err := h.calRepo.GetByID(c.Request.Context(), calID, ownerID); err == pgx.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	shares, err := h.shareRepo.List(c.Request.Context(), calID, ownerID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, shares)
}

func (h *CalendarShareHandler) RemoveShare(c *gin.Context) {
	ownerID := c.MustGet(middleware.UserIDKey).(uuid.UUID)
	calID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	shareID, err := uuid.Parse(c.Param("share_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid share_id"})
		return
	}

	if err := h.shareRepo.Delete(c.Request.Context(), shareID, calID, ownerID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *CalendarShareHandler) ListSharedWithMe(c *gin.Context) {
	userID := c.MustGet(middleware.UserIDKey).(uuid.UUID)
	entries, err := h.shareRepo.ListSharedWithUser(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, entries)
}
