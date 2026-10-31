package handler

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hylin/calendar/internal/middleware"
	"github.com/hylin/calendar/internal/model"
	"github.com/hylin/calendar/internal/repository"
	"github.com/jackc/pgx/v5"
)

var calendarColorRE = regexp.MustCompile(`^#[0-9a-f]{6}$`)

// normalizeCalendarColor trims and lowercases color, as the UI's swatches are, and
// writes a 400 and returns false unless it's #rrggbb: the UI puts it straight into
// CSS, and sharees read it too. Empty (on create: the default) and nil pass.
func normalizeCalendarColor(c *gin.Context, color *string) bool {
	if color == nil {
		return true
	}
	*color = strings.ToLower(strings.TrimSpace(*color))
	if *color != "" && !calendarColorRE.MatchString(*color) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "color must be a hex colour like #4f46e5"})
		return false
	}
	return true
}

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
	// A blank color is fine here: it means the default.
	if !rejectBlank(c, &req.Name, nil) || !normalizeCalendarColor(c, &req.Color) {
		return
	}
	cal, err := h.repo.Create(c.Request.Context(), ownerID, req)
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
	if !rejectBlank(c, req.Name, req.Color) || !normalizeCalendarColor(c, req.Color) {
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
