package middleware

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const UserIDKey = "user_id"

type Claims struct {
	jwt.RegisteredClaims
	UserID uuid.UUID `json:"user_id"`
	// TokenVersion must match the user's current token_version (bumped by a password
	// change); tokens from before this existed carry 0, the column's default.
	TokenVersion int `json:"tv,omitempty"`
}

// minJWTSecretLen is the shortest JWT_SECRET accepted: an HS256 key should be at least
// as long as its 32-byte output.
const minJWTSecretLen = 32

// JWTSecret is the key tokens are signed and verified with, from JWT_SECRET. There is
// deliberately no built-in default: a secret in the source lets anyone mint tokens.
func JWTSecret() []byte {
	return []byte(os.Getenv("JWT_SECRET"))
}

// CheckJWTSecret reports whether JWT_SECRET is usable; the api refuses to start without one.
func CheckJWTSecret() error {
	if n := len(JWTSecret()); n < minJWTSecretLen {
		return fmt.Errorf("JWT_SECRET must be set to at least %d bytes, got %d (generate one with: openssl rand -hex 32)", minJWTSecretLen, n)
	}
	return nil
}

// SessionChecker says whether a token carrying tokenVersion for userID is still valid:
// its user still exists and hasn't changed their password since it was issued.
type SessionChecker interface {
	SessionValid(ctx context.Context, userID uuid.UUID, tokenVersion int) (bool, error)
}

func Auth(sessions SessionChecker) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing token"})
			return
		}
		tokenStr := strings.TrimPrefix(header, "Bearer ")

		var claims Claims
		token, err := jwt.ParseWithClaims(tokenStr, &claims, func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, jwt.ErrSignatureInvalid
			}
			secret := JWTSecret()
			if len(secret) == 0 {
				return nil, jwt.ErrTokenUnverifiable
			}
			return secret, nil
		})
		if err != nil || !token.Valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		ok, err := sessions.SessionValid(c.Request.Context(), claims.UserID, claims.TokenVersion)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "could not check session"})
			return
		}
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "session expired, log in again"})
			return
		}

		c.Set(UserIDKey, claims.UserID)
		c.Next()
	}
}
