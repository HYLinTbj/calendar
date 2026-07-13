package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hylin/calendar/internal/middleware"
	"github.com/hylin/calendar/internal/model"
	"github.com/hylin/calendar/internal/repository"
	"github.com/jackc/pgx/v5"
)

type RecurringEventHandler struct {
	repo    *repository.RecurringEventRepository
	calRepo *repository.CalendarRepository
}

func NewRecurringEventHandler(repo *repository.RecurringEventRepository, calRepo *repository.CalendarRepository) *RecurringEventHandler {
	return &RecurringEventHandler{repo: repo, calRepo: calRepo}
}

func (h *RecurringEventHandler) Create(c *gin.Context) {
	ownerID := c.MustGet(middleware.UserIDKey).(uuid.UUID)
	var req model.CreateRecurringEventRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.EndTime.Before(req.StartTime) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "end_time must be after start_time"})
		return
	}
	calendarID, ok := h.resolveCalendar(c, ownerID, req.CalendarID)
	if !ok {
		return
	}
	rec, err := h.repo.Create(c.Request.Context(), ownerID, calendarID, req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, rec)
}

func (h *RecurringEventHandler) List(c *gin.Context) {
	ownerID := c.MustGet(middleware.UserIDKey).(uuid.UUID)
	recs, err := h.repo.List(c.Request.Context(), ownerID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, recs)
}

func (h *RecurringEventHandler) GetByID(c *gin.Context) {
	ownerID := c.MustGet(middleware.UserIDKey).(uuid.UUID)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	rec, err := h.repo.GetByID(c.Request.Context(), id, ownerID)
	if err == pgx.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, rec)
}

func (h *RecurringEventHandler) Update(c *gin.Context) {
	ownerID := c.MustGet(middleware.UserIDKey).(uuid.UUID)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var req model.UpdateRecurringEventRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.CalendarID != nil {
		if _, err := h.calRepo.GetByID(c.Request.Context(), *req.CalendarID, ownerID); err == pgx.ErrNoRows {
			c.JSON(http.StatusBadRequest, gin.H{"error": "calendar not found"})
			return
		} else if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	rec, err := h.repo.Update(c.Request.Context(), id, ownerID, req)
	if err == pgx.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, rec)
}

func (h *RecurringEventHandler) Delete(c *gin.Context) {
	ownerID := c.MustGet(middleware.UserIDKey).(uuid.UUID)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	if err := h.repo.Delete(c.Request.Context(), id, ownerID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *RecurringEventHandler) resolveCalendar(c *gin.Context, ownerID uuid.UUID, requested *uuid.UUID) (uuid.UUID, bool) {
	if requested != nil {
		cal, err := h.calRepo.GetByID(c.Request.Context(), *requested, ownerID)
		if err == pgx.ErrNoRows {
			c.JSON(http.StatusBadRequest, gin.H{"error": "calendar not found"})
			return uuid.UUID{}, false
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return uuid.UUID{}, false
		}
		return cal.ID, true
	}
	def, err := h.calRepo.GetDefault(c.Request.Context(), ownerID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not resolve default calendar"})
		return uuid.UUID{}, false
	}
	return def.ID, true
}
