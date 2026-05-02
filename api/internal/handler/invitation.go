package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hylin/calendar/api/internal/repository"
	"github.com/jackc/pgx/v5"
)

type InvitationHandler struct {
	repo *repository.InvitationRepository
}

func NewInvitationHandler(repo *repository.InvitationRepository) *InvitationHandler {
	return &InvitationHandler{repo: repo}
}

func (h *InvitationHandler) Accept(c *gin.Context) {
	h.respond(c, "accepted")
}

func (h *InvitationHandler) Decline(c *gin.Context) {
	h.respond(c, "declined")
}

func (h *InvitationHandler) Tentative(c *gin.Context) {
	h.respond(c, "tentative")
}

func (h *InvitationHandler) respond(c *gin.Context, status string) {
	token, err := uuid.Parse(c.Param("token"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid token"})
		return
	}
	inv, err := h.repo.GetByToken(c.Request.Context(), token)
	if err == pgx.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "invitation not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// Allow re-responding so attendees can change their RSVP (e.g. tentative → accepted).
	if inv.Status == status {
		c.JSON(http.StatusOK, gin.H{"status": status})
		return
	}
	if err := h.repo.UpdateStatus(c.Request.Context(), token, status); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": status})
}
