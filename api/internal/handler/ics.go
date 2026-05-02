package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hylin/calendar/api/internal/ics"
	"github.com/hylin/calendar/api/internal/middleware"
	"github.com/hylin/calendar/api/internal/repository"
	"github.com/jackc/pgx/v5"
)

type ICSHandler struct {
	calRepo       *repository.CalendarRepository
	eventRepo     *repository.EventRepository
	recurringRepo *repository.RecurringEventRepository
	inviteRepo    *repository.InvitationRepository
}

func NewICSHandler(
	calRepo *repository.CalendarRepository,
	eventRepo *repository.EventRepository,
	recurringRepo *repository.RecurringEventRepository,
	inviteRepo *repository.InvitationRepository,
) *ICSHandler {
	return &ICSHandler{calRepo: calRepo, eventRepo: eventRepo, recurringRepo: recurringRepo, inviteRepo: inviteRepo}
}

func (h *ICSHandler) Export(c *gin.Context) {
	ownerID := c.MustGet(middleware.UserIDKey).(uuid.UUID)
	calID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	cal, err := h.calRepo.GetByID(c.Request.Context(), calID, ownerID)
	if err == pgx.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	events, err := h.eventRepo.List(c.Request.Context(), ownerID, &calID, nil, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	recurrings, err := h.recurringRepo.ListByCalendar(c.Request.Context(), ownerID, calID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	output := ics.Export(cal.Name, events, recurrings)
	c.Header("Content-Disposition", "attachment; filename=\"calendar.ics\"")
	c.Data(http.StatusOK, "text/calendar; charset=utf-8", []byte(output))
}

func (h *ICSHandler) Import(c *gin.Context) {
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

	events, recurrings, err := ics.Import(calID, c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	imported := 0

	for _, req := range events {
		e, err := h.eventRepo.Create(ctx, ownerID, calID, req)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if len(req.Attendees) > 0 {
			_ = h.inviteRepo.UpsertForEvent(ctx, e.ID, req.Attendees)
		}
		imported++
	}

	for _, req := range recurrings {
		if _, err := h.recurringRepo.Create(ctx, ownerID, calID, req); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		imported++
	}

	c.JSON(http.StatusOK, gin.H{"imported": imported})
}
