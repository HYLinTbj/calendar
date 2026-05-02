package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hylin/calendar/api/internal/middleware"
	"github.com/hylin/calendar/api/internal/model"
	"github.com/hylin/calendar/api/internal/repository"
	"github.com/jackc/pgx/v5"
)

type CalendarHandler struct {
	repo *repository.CalendarRepository
}

func NewCalendarHandler(repo *repository.CalendarRepository) *CalendarHandler {
	return &CalendarHandler{repo: repo}
}

func (h *CalendarHandler) Create(c *gin.Context) {
	ownerID := c.MustGet(middleware.UserIDKey).(uuid.UUID)
	var req model.CreateCalendarRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	cal, err := h.repo.Create(c.Request.Context(), ownerID, req.Name, req.Description)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, cal)
}

func (h *CalendarHandler) List(c *gin.Context) {
	ownerID := c.MustGet(middleware.UserIDKey).(uuid.UUID)
	cals, err := h.repo.List(c.Request.Context(), ownerID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, cals)
}

func (h *CalendarHandler) GetByID(c *gin.Context) {
	ownerID := c.MustGet(middleware.UserIDKey).(uuid.UUID)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	cal, _, err := h.repo.GetByIDOrShared(c.Request.Context(), id, ownerID)
	if err == pgx.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, cal)
}

func (h *CalendarHandler) Update(c *gin.Context) {
	ownerID := c.MustGet(middleware.UserIDKey).(uuid.UUID)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var req model.UpdateCalendarRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	cal, err := h.repo.Update(c.Request.Context(), id, ownerID, req)
	if err == pgx.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, cal)
}

func (h *CalendarHandler) Delete(c *gin.Context) {
	ownerID := c.MustGet(middleware.UserIDKey).(uuid.UUID)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	err = h.repo.Delete(c.Request.Context(), id, ownerID)
	if err == pgx.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if errors.Is(err, repository.ErrDeleteDefault) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot delete the default calendar"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}
