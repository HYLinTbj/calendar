//go:build integration

package repository_test

import (
	"context"
	"testing"

	"github.com/hylin/calendar/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUserRepository_Create(t *testing.T) {
	truncateAll(t, testPool)
	r := repository.NewUserRepository(testPool)
	ctx := context.Background()

	u, err := r.Create(ctx, "alice", "alice@example.com", "hash")
	require.NoError(t, err)
	assert.Equal(t, "alice", u.Username)
	assert.Equal(t, "alice@example.com", u.Email)
	assert.NotEmpty(t, u.ID)
}

func TestUserRepository_Create_DuplicateEmail(t *testing.T) {
	truncateAll(t, testPool)
	r := repository.NewUserRepository(testPool)
	ctx := context.Background()

	_, err := r.Create(ctx, "alice1", "alice@example.com", "hash")
	require.NoError(t, err)

	_, err = r.Create(ctx, "alice2", "alice@example.com", "hash")
	assert.Error(t, err, "should fail on duplicate email")
}

func TestUserRepository_Create_DuplicateUsername(t *testing.T) {
	truncateAll(t, testPool)
	r := repository.NewUserRepository(testPool)
	ctx := context.Background()

	_, err := r.Create(ctx, "alice", "alice1@example.com", "hash")
	require.NoError(t, err)

	_, err = r.Create(ctx, "alice", "alice2@example.com", "hash")
	assert.Error(t, err, "should fail on duplicate username")
}

func TestUserRepository_GetByEmail(t *testing.T) {
	truncateAll(t, testPool)
	r := repository.NewUserRepository(testPool)
	ctx := context.Background()

	_, err := r.Create(ctx, "bob", "bob@example.com", "myhash")
	require.NoError(t, err)

	u, hash, err := r.GetByEmail(ctx, "bob@example.com")
	require.NoError(t, err)
	assert.Equal(t, "bob", u.Username)
	assert.Equal(t, "myhash", hash)
}

func TestUserRepository_GetByID(t *testing.T) {
	truncateAll(t, testPool)
	r := repository.NewUserRepository(testPool)
	ctx := context.Background()

	created, err := r.Create(ctx, "carol", "carol@example.com", "hash")
	require.NoError(t, err)

	u, err := r.GetByID(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, created.ID, u.ID)
	assert.Equal(t, "carol", u.Username)
}

func TestUserRepository_Update_UsernameOnly(t *testing.T) {
	truncateAll(t, testPool)
	r := repository.NewUserRepository(testPool)
	ctx := context.Background()

	created, err := r.Create(ctx, "dave", "dave@example.com", "hash")
	require.NoError(t, err)

	newName := "david"
	updated, err := r.Update(ctx, created.ID, &newName, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "david", updated.Username)
	assert.Equal(t, "dave@example.com", updated.Email)
}

func TestUserRepository_Delete(t *testing.T) {
	truncateAll(t, testPool)
	r := repository.NewUserRepository(testPool)
	ctx := context.Background()

	created, err := r.Create(ctx, "eve", "eve@example.com", "hash")
	require.NoError(t, err)

	err = r.Delete(ctx, created.ID)
	require.NoError(t, err)

	_, err = r.GetByID(ctx, created.ID)
	assert.Error(t, err, "user should be gone")
}
