package repository

import (
	"context"

	"github.com/google/uuid"
	"github.com/hylin/calendar/internal/model"
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
		SELECT id, username, email, password_hash, created_at, updated_at
		FROM users WHERE email = $1
	`, email).Scan(&u.ID, &u.Username, &u.Email, &hash, &u.CreatedAt, &u.UpdatedAt)
	return &u, hash, err
}

func (r *UserRepository) GetByID(ctx context.Context, id uuid.UUID) (*model.User, error) {
	var u model.User
	err := r.pool.QueryRow(ctx, `
		SELECT id, username, email, created_at, updated_at
		FROM users WHERE id = $1
	`, id).Scan(&u.ID, &u.Username, &u.Email, &u.CreatedAt, &u.UpdatedAt)
	return &u, err
}

// Update applies whichever fields are non-nil. passwordHash is already bcrypt-hashed by the caller.
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
			UPDATE users SET username=$1, email=$2, password_hash=$3, updated_at=NOW()
			WHERE id=$4
			RETURNING id, username, email, created_at, updated_at
		`, newUsername, newEmail, *passwordHash, id).
			Scan(&u.ID, &u.Username, &u.Email, &u.CreatedAt, &u.UpdatedAt)
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

func (r *UserRepository) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	return err
}
