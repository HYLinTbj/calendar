//go:build integration

package repository_test

import (
	"context"
	"testing"

	"github.com/hylin/calendar/internal/model"
	"github.com/hylin/calendar/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskRepository_Create(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "task_a")
	r := repository.NewTaskRepository(testPool)

	task, err := r.Create(context.Background(), user.ID, model.CreateTaskRequest{Title: "Buy shoes"})
	require.NoError(t, err)
	assert.Equal(t, "Buy shoes", task.Title)
	assert.False(t, task.Done)
	assert.Nil(t, task.CompletedAt)
}

func TestTaskRepository_List_OpenBeforeDone(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "task_b")
	r := repository.NewTaskRepository(testPool)
	ctx := context.Background()

	done, err := r.Create(ctx, user.ID, model.CreateTaskRequest{Title: "done one"})
	require.NoError(t, err)
	_, err = r.Create(ctx, user.ID, model.CreateTaskRequest{Title: "open one"})
	require.NoError(t, err)

	yes := true
	_, err = r.Update(ctx, done.ID, user.ID, model.UpdateTaskRequest{Done: &yes})
	require.NoError(t, err)

	tasks, err := r.List(ctx, user.ID, nil, nil)
	require.NoError(t, err)
	require.Len(t, tasks, 2)
	// Open tasks sort before done tasks.
	assert.Equal(t, "open one", tasks[0].Title)
	assert.Equal(t, "done one", tasks[1].Title)

	// done filter narrows the result set.
	open, err := r.List(ctx, user.ID, nil, boolPtr(false))
	require.NoError(t, err)
	require.Len(t, open, 1)
	assert.Equal(t, "open one", open[0].Title)
}

func TestTaskRepository_Update_TogglesCompletedAt(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "task_c")
	r := repository.NewTaskRepository(testPool)
	ctx := context.Background()

	task, err := r.Create(ctx, user.ID, model.CreateTaskRequest{Title: "toggle me"})
	require.NoError(t, err)
	require.Nil(t, task.CompletedAt)

	// Mark done -> completed_at set.
	yes := true
	done, err := r.Update(ctx, task.ID, user.ID, model.UpdateTaskRequest{Done: &yes})
	require.NoError(t, err)
	assert.True(t, done.Done)
	require.NotNil(t, done.CompletedAt)

	// Mark not-done -> completed_at cleared.
	no := false
	reopened, err := r.Update(ctx, task.ID, user.ID, model.UpdateTaskRequest{Done: &no})
	require.NoError(t, err)
	assert.False(t, reopened.Done)
	assert.Nil(t, reopened.CompletedAt)
}

func TestTaskRepository_Delete(t *testing.T) {
	truncateAll(t, testPool)
	user := seedUser(t, testPool, "task_d")
	r := repository.NewTaskRepository(testPool)
	ctx := context.Background()

	task, err := r.Create(ctx, user.ID, model.CreateTaskRequest{Title: "rm"})
	require.NoError(t, err)
	require.NoError(t, r.Delete(ctx, task.ID, user.ID))

	_, err = r.GetByID(ctx, task.ID, user.ID)
	assert.Error(t, err)
}

func boolPtr(b bool) *bool { return &b }
