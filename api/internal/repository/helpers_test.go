//go:build integration

package repository_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hylin/calendar/api/internal/model"
	"github.com/hylin/calendar/api/internal/repository"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func truncateAll(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		TRUNCATE event_invitations, calendar_shares, events, recurring_events,
		         categories, calendars, users RESTART IDENTITY
	`)
	require.NoError(t, err)
}

func seedUser(t *testing.T, pool *pgxpool.Pool, suffix string) *model.User {
	t.Helper()
	r := repository.NewUserRepository(pool)
	u, err := r.Create(context.Background(),
		"user_"+suffix,
		fmt.Sprintf("user_%s@example.com", suffix),
		"$2a$10$dummyhashfortesting1234567890abcdef",
	)
	require.NoError(t, err)
	return u
}

func seedCalendar(t *testing.T, pool *pgxpool.Pool, ownerID uuid.UUID, name string) *model.Calendar {
	t.Helper()
	r := repository.NewCalendarRepository(pool)
	c, err := r.Create(context.Background(), ownerID, name, "")
	require.NoError(t, err)
	return c
}

func seedDefaultCalendar(t *testing.T, pool *pgxpool.Pool, ownerID uuid.UUID) *model.Calendar {
	t.Helper()
	r := repository.NewCalendarRepository(pool)
	c, err := r.CreateDefault(context.Background(), ownerID)
	require.NoError(t, err)
	return c
}

func seedEvent(t *testing.T, pool *pgxpool.Pool, ownerID, calendarID uuid.UUID, title string) *model.Event {
	t.Helper()
	r := repository.NewEventRepository(pool)
	e, err := r.Create(context.Background(), ownerID, calendarID, model.CreateEventRequest{
		Title:     title,
		StartTime: time.Date(2024, 6, 15, 9, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2024, 6, 15, 10, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	return e
}

func intPtr(n int) *int { return &n }
