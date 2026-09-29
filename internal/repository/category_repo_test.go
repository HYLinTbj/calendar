//go:build integration

package repository_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/hylin/calendar/internal/model"
	"github.com/hylin/calendar/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCategoryRepository_Create(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "cat_a")
	r := repository.NewCategoryRepository(testPool)
	ctx := context.Background()

	cat, err := r.Create(ctx, user.ID, model.CreateCategoryRequest{Name: "Work", Color: "#FF0000"})
	require.NoError(t, err)
	assert.Equal(t, "Work", cat.Name)
	assert.Equal(t, "#FF0000", cat.Color)
	assert.Equal(t, user.ID, cat.OwnerID)
}

func TestCategoryRepository_Create_UniquePerOwner(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "cat_b")
	r := repository.NewCategoryRepository(testPool)
	ctx := context.Background()

	_, err := r.Create(ctx, user.ID, model.CreateCategoryRequest{Name: "Work", Color: "#FF0000"})
	require.NoError(t, err)

	// Same name for same owner should fail
	_, err = r.Create(ctx, user.ID, model.CreateCategoryRequest{Name: "Work", Color: "#00FF00"})
	assert.Error(t, err, "duplicate category name for same owner should fail")
}

// Concurrent creates that derive the same code must each end up with a distinct
// one, not fail on the unique index.
func TestCategoryRepository_Create_ConcurrentDerivedCodes(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "cat_race")
	r := repository.NewCategoryRepository(testPool)
	ctx := context.Background()

	const n = 8
	start := make(chan struct{})
	codes := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			cat, err := r.Create(ctx, user.ID, model.CreateCategoryRequest{Name: fmt.Sprintf("Chess %d", i), Color: "#FF0000"})
			errs[i] = err
			if err == nil {
				codes[i] = cat.Code
			}
		}()
	}
	close(start)
	wg.Wait()

	seen := map[string]bool{}
	for i := range n {
		require.NoError(t, errs[i], "create %d", i)
		assert.False(t, seen[codes[i]], "code %q assigned twice", codes[i])
		seen[codes[i]] = true
	}
}

func TestCategoryRepository_Create_SameNameDifferentOwner(t *testing.T) {
	truncateAll(t, testPool)
	user1 := seedUser(t, testPool, "cat_c_u1")
	user2 := seedUser(t, testPool, "cat_c_u2")
	r := repository.NewCategoryRepository(testPool)
	ctx := context.Background()

	_, err := r.Create(ctx, user1.ID, model.CreateCategoryRequest{Name: "Work", Color: "#FF0000"})
	require.NoError(t, err)

	// Same name for different owner is allowed
	_, err = r.Create(ctx, user2.ID, model.CreateCategoryRequest{Name: "Work", Color: "#0000FF"})
	require.NoError(t, err)
}

func TestCategoryRepository_GetByID(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "cat_d")
	r := repository.NewCategoryRepository(testPool)
	ctx := context.Background()

	cat, err := r.Create(ctx, user.ID, model.CreateCategoryRequest{Name: "Personal", Color: "#0000FF"})
	require.NoError(t, err)

	got, err := r.GetByID(ctx, cat.ID, user.ID)
	require.NoError(t, err)
	assert.Equal(t, cat.ID, got.ID)
	assert.Equal(t, "Personal", got.Name)
}

func TestCategoryRepository_List(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "cat_e")
	r := repository.NewCategoryRepository(testPool)
	ctx := context.Background()

	_, err := r.Create(ctx, user.ID, model.CreateCategoryRequest{Name: "B", Color: "#000"})
	require.NoError(t, err)
	_, err = r.Create(ctx, user.ID, model.CreateCategoryRequest{Name: "A", Color: "#111"})
	require.NoError(t, err)

	cats, err := r.List(ctx, user.ID)
	require.NoError(t, err)
	require.Len(t, cats, 2)
	// Should be sorted by name ascending
	assert.Equal(t, "A", cats[0].Name)
	assert.Equal(t, "B", cats[1].Name)
}

func TestCategoryRepository_Update(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "cat_f")
	r := repository.NewCategoryRepository(testPool)
	ctx := context.Background()

	cat, err := r.Create(ctx, user.ID, model.CreateCategoryRequest{Name: "Old", Color: "#000"})
	require.NoError(t, err)

	newName := "New"
	newColor := "#FFF"
	updated, err := r.Update(ctx, cat.ID, user.ID, model.UpdateCategoryRequest{Name: &newName, Color: &newColor})
	require.NoError(t, err)
	assert.Equal(t, "New", updated.Name)
	assert.Equal(t, "#FFF", updated.Color)
}

func TestCategoryRepository_Delete(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "cat_g")
	r := repository.NewCategoryRepository(testPool)
	ctx := context.Background()

	cat, err := r.Create(ctx, user.ID, model.CreateCategoryRequest{Name: "Delete Me", Color: "#000"})
	require.NoError(t, err)

	err = r.Delete(ctx, cat.ID, user.ID)
	require.NoError(t, err)

	_, err = r.GetByID(ctx, cat.ID, user.ID)
	assert.Error(t, err)
}
