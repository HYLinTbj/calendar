package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hylin/calendar/internal/middleware"
	"github.com/hylin/calendar/internal/model"
	"github.com/hylin/calendar/internal/repository"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"
)

type UserHandler struct {
	repo *repository.UserRepository
}

func NewUserHandler(repo *repository.UserRepository) *UserHandler {
	return &UserHandler{repo: repo}
}

func (h *UserHandler) GetProfile(c *gin.Context) {
	userID := c.MustGet(middleware.UserIDKey).(uuid.UUID)
	user, err := h.repo.GetByID(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, user)
}

func (h *UserHandler) UpdateProfile(c *gin.Context) {
	userID := c.MustGet(middleware.UserIDKey).(uuid.UUID)
	var req model.UpdateProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// Changing how you sign in needs the current password: a stolen session alone
	// mustn't be able to take the account over.
	if req.Email != nil || req.Password != nil {
		if req.CurrentPassword == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "current_password is required to change email or password"})
			return
		}
		hash, err := h.repo.PasswordHash(c.Request.Context(), userID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not check password"})
			return
		}
		if bcrypt.CompareHashAndPassword([]byte(hash), []byte(*req.CurrentPassword)) != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": "current password is incorrect"})
			return
		}
	}
	if req.Email != nil {
		e := normalizeEmail(*req.Email)
		req.Email = &e
	}
	var hashedPassword *string
	if req.Password != nil {
		if msg := passwordProblem(*req.Password); msg != "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": msg})
			return
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(*req.Password), bcrypt.DefaultCost)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not hash password"})
			return
		}
		s := string(hash)
		hashedPassword = &s
	}
	user, err := h.repo.Update(c.Request.Context(), userID, req.Username, req.Email, hashedPassword)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			c.JSON(http.StatusConflict, gin.H{"error": "username or email already taken"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if hashedPassword == nil {
		c.JSON(http.StatusOK, user)
		return
	}
	// The new password signed out every session, this one included; hand back a fresh
	// token so the caller can carry on.
	token, err := issueToken(user)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not sign token"})
		return
	}
	c.JSON(http.StatusOK, struct {
		*model.User
		Token string `json:"token"`
	}{user, token})
}

func (h *UserHandler) DeleteAccount(c *gin.Context) {
	userID := c.MustGet(middleware.UserIDKey).(uuid.UUID)
	if err := h.repo.Delete(c.Request.Context(), userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}
