//go:build integration

package repository_test

import (
	"context"
	"testing"

	"github.com/hylin/calendar/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCalendarShareRepository_Create(t *testing.T) {
	truncateAll(t, testPool)
	owner := seedUser(t, testPool, "sh_a_owner")
	guest := seedUser(t, testPool, "sh_a_guest")
	cal := seedCalendar(t, testPool, owner.ID, "Shared Cal")
	r := repository.NewCalendarShareRepository(testPool)
	ctx := context.Background()

	share, err := r.Create(ctx, cal.ID, owner.ID, guest.ID, "view")
	require.NoError(t, err)
	assert.Equal(t, "view", share.Permission)
	assert.Equal(t, guest.ID, share.SharedWithUserID)
}

func TestCalendarShareRepository_Create_SelfShare(t *testing.T) {
	truncateAll(t, testPool)
	owner := seedUser(t, testPool, "sh_b")
	cal := seedCalendar(t, testPool, owner.ID, "My Cal")
	r := repository.NewCalendarShareRepository(testPool)
	ctx := context.Background()

	_, err := r.Create(ctx, cal.ID, owner.ID, owner.ID, "view")
	assert.ErrorIs(t, err, repository.ErrSelfShare)
}

func TestCalendarShareRepository_Create_UpsertUpgradesPermission(t *testing.T) {
	truncateAll(t, testPool)
	owner := seedUser(t, testPool, "sh_c_owner")
	guest := seedUser(t, testPool, "sh_c_guest")
	cal := seedCalendar(t, testPool, owner.ID, "Cal")
	r := repository.NewCalendarShareRepository(testPool)
	ctx := context.Background()

	_, err := r.Create(ctx, cal.ID, owner.ID, guest.ID, "view")
	require.NoError(t, err)

	// Upsert with edit permission — should upgrade
	share, err := r.Create(ctx, cal.ID, owner.ID, guest.ID, "edit")
	require.NoError(t, err)
	assert.Equal(t, "edit", share.Permission)
}

func TestCalendarShareRepository_List(t *testing.T) {
	truncateAll(t, testPool)
	owner := seedUser(t, testPool, "sh_d_owner")
	guest1 := seedUser(t, testPool, "sh_d_g1")
	guest2 := seedUser(t, testPool, "sh_d_g2")
	cal := seedCalendar(t, testPool, owner.ID, "Cal")
	r := repository.NewCalendarShareRepository(testPool)
	ctx := context.Background()

	_, err := r.Create(ctx, cal.ID, owner.ID, guest1.ID, "view")
	require.NoError(t, err)
	_, err = r.Create(ctx, cal.ID, owner.ID, guest2.ID, "edit")
	require.NoError(t, err)

	shares, err := r.List(ctx, cal.ID, owner.ID)
	require.NoError(t, err)
	assert.Len(t, shares, 2)
}

func TestCalendarShareRepository_Delete(t *testing.T) {
	truncateAll(t, testPool)
	owner := seedUser(t, testPool, "sh_e_owner")
	guest := seedUser(t, testPool, "sh_e_guest")
	cal := seedCalendar(t, testPool, owner.ID, "Cal")
	r := repository.NewCalendarShareRepository(testPool)
	ctx := context.Background()

	share, err := r.Create(ctx, cal.ID, owner.ID, guest.ID, "view")
	require.NoError(t, err)

	err = r.Delete(ctx, share.ID, cal.ID, owner.ID)
	require.NoError(t, err)

	shares, err := r.List(ctx, cal.ID, owner.ID)
	require.NoError(t, err)
	assert.Empty(t, shares)
}

func TestCalendarShareRepository_GetPermission(t *testing.T) {
	truncateAll(t, testPool)
	owner := seedUser(t, testPool, "sh_f_owner")
	guest := seedUser(t, testPool, "sh_f_guest")
	cal := seedCalendar(t, testPool, owner.ID, "Cal")
	r := repository.NewCalendarShareRepository(testPool)
	ctx := context.Background()

	_, err := r.Create(ctx, cal.ID, owner.ID, guest.ID, "edit")
	require.NoError(t, err)

	perm, err := r.GetPermission(ctx, cal.ID, guest.ID)
	require.NoError(t, err)
	assert.Equal(t, "edit", perm)
}
