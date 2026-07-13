//go:build integration

package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/hylin/calendar/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthHandler_Register_Success(t *testing.T) {
	truncateAll(t, testPool)

	w := Do(t, testRouter, "POST", "/auth/register", "", map[string]string{
		"username": "alice",
		"email":    "alice@example.com",
		"password": "password123",
	})
	assert.Equal(t, http.StatusCreated, w.Code)

	var resp struct {
		ID    uuid.UUID `json:"id"`
		Email string    `json:"email"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "alice@example.com", resp.Email)
	assert.NotEmpty(t, resp.ID)

	// Verify a default calendar was created
	calRepo := repository.NewCalendarRepository(testPool)
	def, err := calRepo.GetDefault(context.Background(), resp.ID)
	require.NoError(t, err)
	assert.True(t, def.IsDefault)
}

func TestAuthHandler_Register_DuplicateEmail(t *testing.T) {
	truncateAll(t, testPool)

	body := map[string]string{
		"username": "alice",
		"email":    "alice@example.com",
		"password": "password123",
	}
	w := Do(t, testRouter, "POST", "/auth/register", "", body)
	require.Equal(t, http.StatusCreated, w.Code)

	body["username"] = "alice2"
	w2 := Do(t, testRouter, "POST", "/auth/register", "", body)
	assert.Equal(t, http.StatusConflict, w2.Code)
}

func TestAuthHandler_Register_MissingFields(t *testing.T) {
	truncateAll(t, testPool)

	w := Do(t, testRouter, "POST", "/auth/register", "", map[string]string{
		"username": "alice",
		// missing email and password
	})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAuthHandler_Login_Success(t *testing.T) {
	truncateAll(t, testPool)

	Do(t, testRouter, "POST", "/auth/register", "", map[string]string{
		"username": "bob",
		"email":    "bob@example.com",
		"password": "password123",
	})

	w := Do(t, testRouter, "POST", "/auth/login", "", map[string]string{
		"email":    "bob@example.com",
		"password": "password123",
	})
	assert.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Token string `json:"token"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.NotEmpty(t, resp.Token)
}

func TestAuthHandler_Login_WrongPassword(t *testing.T) {
	truncateAll(t, testPool)

	Do(t, testRouter, "POST", "/auth/register", "", map[string]string{
		"username": "carol",
		"email":    "carol@example.com",
		"password": "password123",
	})

	w := Do(t, testRouter, "POST", "/auth/login", "", map[string]string{
		"email":    "carol@example.com",
		"password": "wrongpassword",
	})
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAuthHandler_Login_UnknownEmail(t *testing.T) {
	truncateAll(t, testPool)

	w := Do(t, testRouter, "POST", "/auth/login", "", map[string]string{
		"email":    "nobody@example.com",
		"password": "password123",
	})
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
