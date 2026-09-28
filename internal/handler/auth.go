package handler

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/hylin/calendar/internal/middleware"
	"github.com/hylin/calendar/internal/model"
	"github.com/hylin/calendar/internal/repository"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"
)

type AuthHandler struct {
	userRepo *repository.UserRepository
	calRepo  *repository.CalendarRepository
}

func NewAuthHandler(userRepo *repository.UserRepository, calRepo *repository.CalendarRepository) *AuthHandler {
	return &AuthHandler{userRepo: userRepo, calRepo: calRepo}
}

func (h *AuthHandler) Register(c *gin.Context) {
	var req model.RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if msg := passwordProblem(req.Password); msg != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
		return
	}
	req.Email = normalizeEmail(req.Email)
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not hash password"})
		return
	}
	user, err := h.userRepo.Create(c.Request.Context(), req.Username, req.Email, string(hash))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			c.JSON(http.StatusConflict, gin.H{"error": "username or email already taken"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if _, err := h.calRepo.CreateDefault(c.Request.Context(), user.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not create default calendar"})
		return
	}
	c.JSON(http.StatusCreated, user)
}

// normalizeEmail is how emails are stored: they're case-insensitive (see Migrate).
func normalizeEmail(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// issueToken signs a login token for user u, valid for 24h and until their password
// next changes (TokenVersion).
func issueToken(u *model.User) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, middleware.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
		UserID:       u.ID,
		TokenVersion: u.TokenVersion,
	})
	return token.SignedString(middleware.JWTSecret())
}

// passwordProblem returns why pw can't be used, or "" if it can. bcrypt hashes at most
// 72 bytes and rejects longer input, which would otherwise surface as a 500.
func passwordProblem(pw string) string {
	switch {
	case len(pw) < 8:
		return "password must be at least 8 characters"
	case len(pw) > 72:
		return "password must be at most 72 bytes"
	}
	return ""
}

func (h *AuthHandler) Login(c *gin.Context) {
	var req model.LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	user, hash, err := h.userRepo.GetByEmail(c.Request.Context(), normalizeEmail(req.Email))
	if err == pgx.ErrNoRows {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}
	signed, err := issueToken(user)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not sign token"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"token": signed, "user": user})
}
