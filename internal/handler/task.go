package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hylin/calendar/internal/middleware"
	"github.com/hylin/calendar/internal/model"
	"github.com/hylin/calendar/internal/repository"
	"github.com/jackc/pgx/v5"
)

type TaskHandler struct {
	repo    *repository.TaskRepository
	catRepo *repository.CategoryRepository
}

func NewTaskHandler(repo *repository.TaskRepository, catRepo *repository.CategoryRepository) *TaskHandler {
	return &TaskHandler{repo: repo, catRepo: catRepo}
}

func (h *TaskHandler) Create(c *gin.Context) {
	ownerID := c.MustGet(middleware.UserIDKey).(uuid.UUID)
	var req model.CreateTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if !validateCategoryOwnership(c, h.catRepo, ownerID, req.AreaID, "area not found") {
		return
	}
	task, err := h.repo.Create(c.Request.Context(), ownerID, req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, task)
}

func (h *TaskHandler) List(c *gin.Context) {
	ownerID := c.MustGet(middleware.UserIDKey).(uuid.UUID)

	var areaID *uuid.UUID
	if v := c.Query("area_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid area_id"})
			return
		}
		areaID = &id
	}

	var done *bool
	if v := c.Query("done"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid 'done', use true or false"})
			return
		}
		done = &b
	}

	tasks, err := h.repo.List(c.Request.Context(), ownerID, areaID, done)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, tasks)
}

func (h *TaskHandler) GetByID(c *gin.Context) {
	ownerID := c.MustGet(middleware.UserIDKey).(uuid.UUID)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	task, err := h.repo.GetByID(c.Request.Context(), id, ownerID)
	if err == pgx.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, task)
}

func (h *TaskHandler) Update(c *gin.Context) {
	ownerID := c.MustGet(middleware.UserIDKey).(uuid.UUID)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var req model.UpdateTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// AreaID is Optional: an absent field keeps the current area (skip), an explicit
	// null clears it (nil is valid), and a concrete id must exist and be owned.
	if req.AreaID.Set && !validateCategoryOwnership(c, h.catRepo, ownerID, req.AreaID.Value, "area not found") {
		return
	}
	task, err := h.repo.Update(c.Request.Context(), id, ownerID, req)
	if err == pgx.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, task)
}

func (h *TaskHandler) Delete(c *gin.Context) {
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
