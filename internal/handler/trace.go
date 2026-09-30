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

// maxTraceListDays bounds GET /traces so one request can't scan years of rows.
const maxTraceListDays = 400

type TraceHandler struct {
	repo    *repository.TraceRepository
	catRepo *repository.CategoryRepository
}

func NewTraceHandler(repo *repository.TraceRepository, catRepo *repository.CategoryRepository) *TraceHandler {
	return &TraceHandler{repo: repo, catRepo: catRepo}
}

func (h *TraceHandler) Create(c *gin.Context) {
	ownerID := c.MustGet(middleware.UserIDKey).(uuid.UUID)
	var req model.CreateTraceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if !validateCategoryOwnership(c, h.catRepo, ownerID, req.CategoryID, "area not found") {
		return
	}
	trace, err := h.repo.Create(c.Request.Context(), ownerID, req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, trace)
}

// List handles GET /traces?from=YYYY-MM-DD&to=YYYY-MM-DD (both days inclusive).
func (h *TraceHandler) List(c *gin.Context) {
	ownerID := c.MustGet(middleware.UserIDKey).(uuid.UUID)
	from, err := model.ParseDate(c.Query("from"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid 'from', use YYYY-MM-DD"})
		return
	}
	to, err := model.ParseDate(c.Query("to"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid 'to', use YYYY-MM-DD"})
		return
	}
	if to.Before(from) || to.Sub(from).Hours()/24 >= maxTraceListDays {
		c.JSON(http.StatusBadRequest, gin.H{"error": "'to' must be on or after 'from', at most 400 days apart"})
		return
	}
	traces, err := h.repo.List(c.Request.Context(), ownerID, model.Date(c.Query("from")), model.Date(c.Query("to")))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, traces)
}

func (h *TraceHandler) GetByID(c *gin.Context) {
	ownerID := c.MustGet(middleware.UserIDKey).(uuid.UUID)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	trace, err := h.repo.GetByID(c.Request.Context(), id, ownerID)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, trace)
}

func (h *TraceHandler) Update(c *gin.Context) {
	ownerID := c.MustGet(middleware.UserIDKey).(uuid.UUID)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var req model.UpdateTraceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Minutes != nil && (*req.Minutes < 1 || *req.Minutes > model.MaxTraceMinutes) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "minutes must be between 1 and 1440"})
		return
	}
	if req.CategoryID.Set && !validateCategoryOwnership(c, h.catRepo, ownerID, req.CategoryID.Value, "area not found") {
		return
	}
	trace, err := h.repo.Update(c.Request.Context(), id, ownerID, req)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, trace)
}

func (h *TraceHandler) Delete(c *gin.Context) {
	ownerID := c.MustGet(middleware.UserIDKey).(uuid.UUID)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	err = h.repo.Delete(c.Request.Context(), id, ownerID)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}
