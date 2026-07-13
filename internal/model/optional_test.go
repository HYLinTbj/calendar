package model_test

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/hylin/calendar/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOptional_AbsentNullValue(t *testing.T) {
	type payload struct {
		CategoryID model.Optional[uuid.UUID] `json:"category_id"`
	}

	// Absent field: not set, keep current value.
	var absent payload
	require.NoError(t, json.Unmarshal([]byte(`{}`), &absent))
	assert.False(t, absent.CategoryID.Set)

	// Explicit null: set with nil value, clears.
	var null payload
	require.NoError(t, json.Unmarshal([]byte(`{"category_id":null}`), &null))
	assert.True(t, null.CategoryID.Set)
	assert.Nil(t, null.CategoryID.Value)

	// Concrete value: set with the decoded value.
	id := uuid.New()
	var val payload
	require.NoError(t, json.Unmarshal([]byte(`{"category_id":"`+id.String()+`"}`), &val))
	assert.True(t, val.CategoryID.Set)
	require.NotNil(t, val.CategoryID.Value)
	assert.Equal(t, id, *val.CategoryID.Value)

	// Malformed value still errors.
	var bad payload
	assert.Error(t, json.Unmarshal([]byte(`{"category_id":"not-a-uuid"}`), &bad))
}
