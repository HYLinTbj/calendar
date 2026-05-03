//go:build integration

package repository_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/hylin/calendar/api/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func getInvitationToken(t *testing.T, eventID uuid.UUID, email string) uuid.UUID {
	t.Helper()
	var tokenStr string
	err := testPool.QueryRow(context.Background(),
		`SELECT token::text FROM event_invitations WHERE event_id=$1 AND email=$2`,
		eventID, email).Scan(&tokenStr)
	require.NoError(t, err)
	token, err := uuid.Parse(tokenStr)
	require.NoError(t, err)
	return token
}

func TestInvitationRepository_UpsertForEvent_Idempotent(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "inv_a")
	cal := seedCalendar(t, testPool, user.ID, "Cal")
	ev := seedEvent(t, testPool, user.ID, cal.ID, "Event")
	r := repository.NewInvitationRepository(testPool)
	ctx := context.Background()

	emails := []string{"alice@example.com", "bob@example.com"}

	err := r.UpsertForEvent(ctx, ev.ID, emails)
	require.NoError(t, err)

	// Second call should not error (ON CONFLICT DO NOTHING)
	err = r.UpsertForEvent(ctx, ev.ID, emails)
	require.NoError(t, err)

	statuses, err := r.ListStatusesByEvent(ctx, ev.ID)
	require.NoError(t, err)
	assert.Len(t, statuses, 2)
}

func TestInvitationRepository_ListStatusesByEvent_NormalizesStatus(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "inv_b")
	cal := seedCalendar(t, testPool, user.ID, "Cal")
	ev := seedEvent(t, testPool, user.ID, cal.ID, "Event")
	r := repository.NewInvitationRepository(testPool)
	ctx := context.Background()

	err := r.UpsertForEvent(ctx, ev.ID, []string{"alice@example.com"})
	require.NoError(t, err)

	statuses, err := r.ListStatusesByEvent(ctx, ev.ID)
	require.NoError(t, err)
	require.Len(t, statuses, 1)
	// pending_send maps to "needs_action"
	assert.Equal(t, "needs_action", statuses[0].Status)
}

func TestInvitationRepository_UpdateStatus_AcceptedIsReflected(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "inv_c")
	cal := seedCalendar(t, testPool, user.ID, "Cal")
	ev := seedEvent(t, testPool, user.ID, cal.ID, "Event")
	r := repository.NewInvitationRepository(testPool)
	ctx := context.Background()

	err := r.UpsertForEvent(ctx, ev.ID, []string{"alice@example.com"})
	require.NoError(t, err)

	token := getInvitationToken(t, ev.ID, "alice@example.com")

	err = r.UpdateStatus(ctx, token, "accepted")
	require.NoError(t, err)

	statuses, err := r.ListStatusesByEvent(ctx, ev.ID)
	require.NoError(t, err)
	require.Len(t, statuses, 1)
	assert.Equal(t, "accepted", statuses[0].Status)
}

func TestInvitationRepository_ReInviteChanged_OnlyResetsSent(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "inv_d")
	cal := seedCalendar(t, testPool, user.ID, "Cal")
	ev := seedEvent(t, testPool, user.ID, cal.ID, "Event")
	r := repository.NewInvitationRepository(testPool)
	ctx := context.Background()

	emails := []string{"alice@example.com", "bob@example.com"}
	err := r.UpsertForEvent(ctx, ev.ID, emails)
	require.NoError(t, err)

	// Set alice to 'sent' and bob to 'accepted'
	_, err = testPool.Exec(ctx,
		`UPDATE event_invitations SET status='sent' WHERE event_id=$1 AND email=$2`,
		ev.ID, "alice@example.com")
	require.NoError(t, err)
	_, err = testPool.Exec(ctx,
		`UPDATE event_invitations SET status='accepted' WHERE event_id=$1 AND email=$2`,
		ev.ID, "bob@example.com")
	require.NoError(t, err)

	err = r.ReInviteChanged(ctx, ev.ID, emails)
	require.NoError(t, err)

	var aliceStatus, bobStatus string
	err = testPool.QueryRow(ctx,
		`SELECT status FROM event_invitations WHERE event_id=$1 AND email=$2`,
		ev.ID, "alice@example.com").Scan(&aliceStatus)
	require.NoError(t, err)
	assert.Equal(t, "pending_send", aliceStatus, "sent should be reset to pending_send")

	err = testPool.QueryRow(ctx,
		`SELECT status FROM event_invitations WHERE event_id=$1 AND email=$2`,
		ev.ID, "bob@example.com").Scan(&bobStatus)
	require.NoError(t, err)
	assert.Equal(t, "accepted", bobStatus, "accepted must not be reset")
}

func TestInvitationRepository_RemoveAbsent(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "inv_e")
	cal := seedCalendar(t, testPool, user.ID, "Cal")
	ev := seedEvent(t, testPool, user.ID, cal.ID, "Event")
	r := repository.NewInvitationRepository(testPool)
	ctx := context.Background()

	err := r.UpsertForEvent(ctx, ev.ID, []string{"alice@example.com", "bob@example.com", "carol@example.com"})
	require.NoError(t, err)

	err = r.RemoveAbsent(ctx, ev.ID, []string{"alice@example.com", "bob@example.com"})
	require.NoError(t, err)

	statuses, err := r.ListStatusesByEvent(ctx, ev.ID)
	require.NoError(t, err)
	assert.Len(t, statuses, 2)
	gotEmails := make([]string, len(statuses))
	for i, s := range statuses {
		gotEmails[i] = s.Email
	}
	assert.Contains(t, gotEmails, "alice@example.com")
	assert.Contains(t, gotEmails, "bob@example.com")
	assert.NotContains(t, gotEmails, "carol@example.com")
}

func TestInvitationRepository_GetByToken(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "inv_f")
	cal := seedCalendar(t, testPool, user.ID, "Cal")
	ev := seedEvent(t, testPool, user.ID, cal.ID, "Event")
	r := repository.NewInvitationRepository(testPool)
	ctx := context.Background()

	err := r.UpsertForEvent(ctx, ev.ID, []string{"alice@example.com"})
	require.NoError(t, err)

	token := getInvitationToken(t, ev.ID, "alice@example.com")

	inv, err := r.GetByToken(ctx, token)
	require.NoError(t, err)
	assert.Equal(t, ev.ID, inv.EventID)
	assert.Equal(t, "alice@example.com", inv.Email)
}
