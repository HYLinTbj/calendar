//go:build integration

package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/hylin/calendar/internal/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func truncateAll(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		TRUNCATE tasks, event_invitations, calendar_shares, events,
		         recurring_events, categories, calendars, users RESTART IDENTITY
	`)
	require.NoError(t, err)
	require.NoError(t, testRDB.FlushDB(context.Background()).Err())
}

// MintJWT returns a signed JWT for the given userID using the default dev secret.
func MintJWT(t *testing.T, userID uuid.UUID) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, middleware.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
		UserID: userID,
	})
	signed, err := token.SignedString(middleware.JWTSecret())
	require.NoError(t, err)
	return signed
}

// Do sends an HTTP request through the router and returns the recorder.
func Do(t *testing.T, router *gin.Engine, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var bodyReader *bytes.Reader
	if body != nil {
		data, err := json.Marshal(body)
		require.NoError(t, err)
		bodyReader = bytes.NewReader(data)
	} else {
		bodyReader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, bodyReader)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// MustRegisterAndLogin creates a user via the API and returns (userID, jwtToken).
func MustRegisterAndLogin(t *testing.T, router *gin.Engine, suffix string) (uuid.UUID, string) {
	t.Helper()
	username := "user_" + suffix
	email := fmt.Sprintf("user_%s@example.com", suffix)
	password := "password123"

	w := Do(t, router, "POST", "/auth/register", "", map[string]string{
		"username": username,
		"email":    email,
		"password": password,
	})
	require.Equal(t, http.StatusCreated, w.Code, "register failed: %s", w.Body.String())

	var regResp struct {
		ID uuid.UUID `json:"id"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&regResp))

	w2 := Do(t, router, "POST", "/auth/login", "", map[string]string{
		"email":    email,
		"password": password,
	})
	require.Equal(t, http.StatusOK, w2.Code, "login failed: %s", w2.Body.String())

	var loginResp struct {
		Token string `json:"token"`
	}
	require.NoError(t, json.NewDecoder(w2.Body).Decode(&loginResp))
	return regResp.ID, loginResp.Token
}

// createCategory creates a category (Area) via the API and returns its ID.
// weeklyTarget sets weekly_target_minutes so stats tests can assert against it.
func createCategory(t *testing.T, token, name string, weeklyTarget int) uuid.UUID {
	t.Helper()
	w := Do(t, testRouter, "POST", "/categories", token, map[string]any{
		"name":                  name,
		"color":                 "#4285F4",
		"weekly_target_minutes": weeklyTarget,
	})
	require.Equal(t, http.StatusCreated, w.Code, "create category failed: %s", w.Body.String())
	var resp struct {
		ID uuid.UUID `json:"id"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	return resp.ID
}
