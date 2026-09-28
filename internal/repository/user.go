package repository

import (
	"context"

	"github.com/google/uuid"
	"github.com/hylin/calendar/internal/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type UserRepository struct {
	pool *pgxpool.Pool
}

func NewUserRepository(pool *pgxpool.Pool) *UserRepository {
	return &UserRepository{pool: pool}
}

func (r *UserRepository) Create(ctx context.Context, username, email, passwordHash string) (*model.User, error) {
	var u model.User
	err := r.pool.QueryRow(ctx, `
		INSERT INTO users (username, email, password_hash)
		VALUES ($1, $2, $3)
		RETURNING id, username, email, created_at, updated_at
	`, username, email, passwordHash).
		Scan(&u.ID, &u.Username, &u.Email, &u.CreatedAt, &u.UpdatedAt)
	return &u, err
}

// GetByEmail returns the user and their stored password hash for login.
func (r *UserRepository) GetByEmail(ctx context.Context, email string) (*model.User, string, error) {
	var u model.User
	var hash string
	err := r.pool.QueryRow(ctx, `
		SELECT id, username, email, password_hash, created_at, updated_at, token_version
		FROM users WHERE lower(email) = lower($1)
		ORDER BY (email = $1) DESC, created_at
		LIMIT 1
	`, email).Scan(&u.ID, &u.Username, &u.Email, &hash, &u.CreatedAt, &u.UpdatedAt, &u.TokenVersion)
	return &u, hash, err
}

// PasswordHash returns the user's stored bcrypt password hash.
func (r *UserRepository) PasswordHash(ctx context.Context, id uuid.UUID) (string, error) {
	var hash string
	err := r.pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE id = $1`, id).Scan(&hash)
	return hash, err
}

// SessionValid reports whether a token carrying tokenVersion for user id is still good:
// the user exists and hasn't changed their password since it was issued.
func (r *UserRepository) SessionValid(ctx context.Context, id uuid.UUID, tokenVersion int) (bool, error) {
	var current int
	err := r.pool.QueryRow(ctx, `SELECT token_version FROM users WHERE id = $1`, id).Scan(&current)
	if err == pgx.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return tokenVersion == current, nil
}

func (r *UserRepository) GetByID(ctx context.Context, id uuid.UUID) (*model.User, error) {
	var u model.User
	err := r.pool.QueryRow(ctx, `
		SELECT id, username, email, created_at, updated_at
		FROM users WHERE id = $1
	`, id).Scan(&u.ID, &u.Username, &u.Email, &u.CreatedAt, &u.UpdatedAt)
	return &u, err
}

// Update applies whichever fields are non-nil. passwordHash is already bcrypt-hashed by
// the caller. A new password also bumps token_version, invalidating every token issued
// before it (see SessionValid); the returned user carries the new version.
func (r *UserRepository) Update(ctx context.Context, id uuid.UUID, username, email, passwordHash *string) (*model.User, error) {
	current, err := r.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	newUsername := current.Username
	newEmail := current.Email
	if username != nil {
		newUsername = *username
	}
	if email != nil {
		newEmail = *email
	}

	var u model.User
	if passwordHash != nil {
		err = r.pool.QueryRow(ctx, `
			UPDATE users SET username=$1, email=$2, password_hash=$3,
			    token_version=token_version+1, updated_at=NOW()
			WHERE id=$4
			RETURNING id, username, email, created_at, updated_at, token_version
		`, newUsername, newEmail, *passwordHash, id).
			Scan(&u.ID, &u.Username, &u.Email, &u.CreatedAt, &u.UpdatedAt, &u.TokenVersion)
	} else {
		err = r.pool.QueryRow(ctx, `
			UPDATE users SET username=$1, email=$2, updated_at=NOW()
			WHERE id=$3
			RETURNING id, username, email, created_at, updated_at
		`, newUsername, newEmail, id).
			Scan(&u.ID, &u.Username, &u.Email, &u.CreatedAt, &u.UpdatedAt)
	}
	return &u, err
}

// Delete removes the user and everything in their calendars. Events and series other
// users (edit-share collaborators) put in those calendars don't cascade with the user
// and would block deleting the calendars (calendar_id is ON DELETE RESTRICT), so they
// go first, in the same transaction.
func (r *UserRepository) Delete(ctx context.Context, id uuid.UUID) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, q := range []string{
		`DELETE FROM events WHERE calendar_id IN (SELECT id FROM calendars WHERE owner_id = $1)`,
		`DELETE FROM recurring_events WHERE calendar_id IN (SELECT id FROM calendars WHERE owner_id = $1)`,
		`DELETE FROM users WHERE id = $1`,
	} {
		if _, err := tx.Exec(ctx, q, id); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
