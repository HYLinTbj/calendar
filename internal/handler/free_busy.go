package handler

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hylin/calendar/internal/middleware"
	"github.com/hylin/calendar/internal/model"
	"github.com/hylin/calendar/internal/repository"
	"github.com/jackc/pgx/v5"
)

type FreeBusyHandler struct {
	eventRepo *repository.EventRepository
	userRepo  *repository.UserRepository
}

func NewFreeBusyHandler(eventRepo *repository.EventRepository, userRepo *repository.UserRepository) *FreeBusyHandler {
	return &FreeBusyHandler{eventRepo: eventRepo, userRepo: userRepo}
}

func (h *FreeBusyHandler) Query(c *gin.Context) {
	requesterID := c.MustGet(middleware.UserIDKey).(uuid.UUID)

	rawEmails := c.Query("emails")
	if rawEmails == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "emails is required"})
		return
	}
	fromStr := c.Query("from")
	toStr := c.Query("to")
	if fromStr == "" || toStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "from and to are required"})
		return
	}
	from, err := time.Parse(time.RFC3339, fromStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid from, use RFC3339"})
		return
	}
	to, err := time.Parse(time.RFC3339, toStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid to, use RFC3339"})
		return
	}
	if !to.After(from) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "to must be after from"})
		return
	}

	emails := splitEmails(rawEmails)
	if len(emails) > 50 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "too many emails (max 50)"})
		return
	}

	ctx := c.Request.Context()
	results := make([]model.FreeBusyEntry, 0, len(emails))

	for _, email := range emails {
		entry := model.FreeBusyEntry{Email: email, Busy: []model.TimeSlot{}}

		target, _, err := h.userRepo.GetByEmail(ctx, email)
		if err == pgx.ErrNoRows {
			entry.Status = "not_found"
			results = append(results, entry)
			continue
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		slots, hasAccess, err := h.eventRepo.GetBusySlots(ctx, requesterID, target.ID, from, to)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if !hasAccess {
			entry.Status = "not_shared"
			results = append(results, entry)
			continue
		}
		entry.Busy = slots
		results = append(results, entry)
	}

	c.JSON(http.StatusOK, results)
}

func splitEmails(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
