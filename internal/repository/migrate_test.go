//go:build integration

package repository_test

import (
	"context"
	"testing"

	"github.com/hylin/calendar/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMigrate_BackfillsCategoryCodes simulates Areas created before the code
// column existed (code NULL) and checks that re-running Migrate derives unique
// per-owner codes and restores NOT NULL.
func TestMigrate_BackfillsCategoryCodes(t *testing.T) {
	truncateAll(t, testPool)
	ctx := context.Background()
	a := seedUser(t, testPool, "mig_a")
	b := seedUser(t, testPool, "mig_b")

	_, err := testPool.Exec(ctx, `
		DROP INDEX categories_owner_code_uniq;
		ALTER TABLE categories ALTER COLUMN code DROP NOT NULL`)
	require.NoError(t, err)
	for _, row := range []struct {
		owner any
		name  string
	}{
		{a.ID, "Calendar app"}, {a.ID, "Carpentry"}, {a.ID, "Café"}, {a.ID, "123"}, {b.ID, "Calendar app"},
	} {
		_, err := testPool.Exec(ctx,
			`INSERT INTO categories (owner_id, name, color, code) VALUES ($1, $2, '#000000', NULL)`, row.owner, row.name)
		require.NoError(t, err)
	}

	require.NoError(t, db.Migrate(ctx, testPool))

	codes := func(owner any) map[string]string {
		rows, err := testPool.Query(ctx, `SELECT name, code FROM categories WHERE owner_id=$1`, owner)
		require.NoError(t, err)
		defer rows.Close()
		m := map[string]string{}
		for rows.Next() {
			var name, code string
			require.NoError(t, rows.Scan(&name, &code))
			m[name] = code
		}
		return m
	}
	ca := codes(a.ID)
	assert.Equal(t, "X", ca["123"])
	// The three "CA…" names share a base; each gets a distinct code.
	got := map[string]bool{ca["Calendar app"]: true, ca["Carpentry"]: true, ca["Café"]: true}
	assert.Equal(t, map[string]bool{"CA": true, "CA2": true, "CA3": true}, got)
	assert.Equal(t, "CA", codes(b.ID)["Calendar app"])

	// NOT NULL and the unique index are back.
	_, err = testPool.Exec(ctx, `INSERT INTO categories (owner_id, name, color) VALUES ($1, 'Nope', '#000000')`, a.ID)
	assert.Error(t, err)
	_, err = testPool.Exec(ctx, `INSERT INTO categories (owner_id, name, color, code) VALUES ($1, 'Dup', '#000000', 'CA')`, a.ID)
	assert.Error(t, err)
}
