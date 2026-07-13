package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/hylin/calendar/internal/model"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Invitation struct {
	ID        uuid.UUID
	EventID   uuid.UUID
	Email     string
	Status    string
	Token     uuid.UUID
	CreatedAt time.Time
	UpdatedAt time.Time
}

type InvitationRepository struct {
	pool *pgxpool.Pool
}

func NewInvitationRepository(pool *pgxpool.Pool) *InvitationRepository {
	return &InvitationRepository{pool: pool}
}

// UpsertForEvent inserts invitation rows for each email. Existing rows
// (already sent/accepted/declined) are left unchanged.
func (r *InvitationRepository) UpsertForEvent(ctx context.Context, eventID uuid.UUID, emails []string) error {
	if len(emails) == 0 {
		return nil
	}
	for _, email := range emails {
		_, err := r.pool.Exec(ctx, `
			INSERT INTO event_invitations (event_id, email)
			VALUES ($1, $2)
			ON CONFLICT (event_id, email) DO NOTHING`,
			eventID, email)
		if err != nil {
			return err
		}
	}
	return nil
}

// ReInviteChanged resets attendees to pending_send when event details change
// so they receive an updated invite. Only affects rows already in 'sent' state
// (not accepted/declined — those people made a decision).
func (r *InvitationRepository) ReInviteChanged(ctx context.Context, eventID uuid.UUID, emails []string) error {
	if len(emails) == 0 {
		return nil
	}
	_, err := r.pool.Exec(ctx, `
		UPDATE event_invitations
		SET status = 'pending_send', updated_at = NOW()
		WHERE event_id = $1
		  AND email = ANY($2)
		  AND status = 'sent'`,
		eventID, emails)
	return err
}

// RemoveAbsent deletes invitation rows for emails no longer in the attendee list.
func (r *InvitationRepository) RemoveAbsent(ctx context.Context, eventID uuid.UUID, keepEmails []string) error {
	_, err := r.pool.Exec(ctx, `
		DELETE FROM event_invitations
		WHERE event_id = $1 AND NOT (email = ANY($2))`,
		eventID, keepEmails)
	return err
}

// GetByToken fetches the invitation and its associated event details for the
// notification worker and accept/decline handler.
func (r *InvitationRepository) GetByToken(ctx context.Context, token uuid.UUID) (*Invitation, error) {
	var inv Invitation
	err := r.pool.QueryRow(ctx, `
		SELECT id, event_id, email, status, token, created_at, updated_at
		FROM event_invitations WHERE token = $1`, token).
		Scan(&inv.ID, &inv.EventID, &inv.Email, &inv.Status, &inv.Token, &inv.CreatedAt, &inv.UpdatedAt)
	return &inv, err
}

// UpdateStatus sets the invitation status (accepted / declined / tentative).
func (r *InvitationRepository) UpdateStatus(ctx context.Context, token uuid.UUID, status string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE event_invitations SET status=$1, updated_at=NOW() WHERE token=$2`,
		status, token)
	return err
}

// ListStatusesByEvent returns per-attendee RSVP statuses for an event.
// Internal statuses (pending_send, sent) are surfaced as "needs_action".
func (r *InvitationRepository) ListStatusesByEvent(ctx context.Context, eventID uuid.UUID) ([]model.AttendeeStatus, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT email, status FROM event_invitations WHERE event_id = $1 ORDER BY email`,
		eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var statuses []model.AttendeeStatus
	for rows.Next() {
		var s model.AttendeeStatus
		var raw string
		if err := rows.Scan(&s.Email, &raw); err != nil {
			return nil, err
		}
		switch raw {
		case "accepted", "declined", "tentative":
			s.Status = raw
		default:
			s.Status = "needs_action"
		}
		statuses = append(statuses, s)
	}
	return statuses, rows.Err()
}
