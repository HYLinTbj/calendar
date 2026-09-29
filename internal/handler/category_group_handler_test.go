//go:build integration

package handler_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hylin/calendar/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func decodeCategory(t *testing.T, body []byte) model.Category {
	t.Helper()
	var cat model.Category
	require.NoError(t, json.Unmarshal(body, &cat))
	return cat
}

func TestCategoryGroupHandler_CRUD(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "grp_crud")

	w := Do(t, testRouter, "POST", "/category-groups", token, map[string]any{
		"name": "Language learning", "color": "#2A56C6", "position": 1,
	})
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var g model.CategoryGroup
	require.NoError(t, json.NewDecoder(w.Body).Decode(&g))
	assert.Equal(t, "Language learning", g.Name)
	assert.Equal(t, "#2A56C6", g.Color)
	assert.Equal(t, 1, g.Position)

	// Duplicate name for the same owner → 409.
	wDup := Do(t, testRouter, "POST", "/category-groups", token, map[string]any{
		"name": "Language learning", "color": "#000000",
	})
	assert.Equal(t, http.StatusConflict, wDup.Code, wDup.Body.String())

	// Missing colour → 400.
	wBad := Do(t, testRouter, "POST", "/category-groups", token, map[string]any{"name": "Health"})
	assert.Equal(t, http.StatusBadRequest, wBad.Code, wBad.Body.String())

	// List orders by position, then name.
	createCategoryGroup(t, token, "Health", "#1F6B5C") // position 0
	wList := Do(t, testRouter, "GET", "/category-groups", token, nil)
	require.Equal(t, http.StatusOK, wList.Code)
	var groups []model.CategoryGroup
	require.NoError(t, json.NewDecoder(wList.Body).Decode(&groups))
	require.Len(t, groups, 2)
	assert.Equal(t, "Health", groups[0].Name)
	assert.Equal(t, "Language learning", groups[1].Name)

	// Partial update keeps untouched fields.
	wUpd := Do(t, testRouter, "PUT", fmt.Sprintf("/category-groups/%s", g.ID), token, map[string]any{
		"name": "Languages",
	})
	require.Equal(t, http.StatusOK, wUpd.Code, wUpd.Body.String())
	var upd model.CategoryGroup
	require.NoError(t, json.NewDecoder(wUpd.Body).Decode(&upd))
	assert.Equal(t, "Languages", upd.Name)
	assert.Equal(t, "#2A56C6", upd.Color)
	assert.Equal(t, 1, upd.Position)

	// Renaming onto another group's name → 409.
	wClash := Do(t, testRouter, "PUT", fmt.Sprintf("/category-groups/%s", g.ID), token, map[string]any{
		"name": "Health",
	})
	assert.Equal(t, http.StatusConflict, wClash.Code, wClash.Body.String())

	wDel := Do(t, testRouter, "DELETE", fmt.Sprintf("/category-groups/%s", g.ID), token, nil)
	assert.Equal(t, http.StatusNoContent, wDel.Code)
	wGet := Do(t, testRouter, "GET", fmt.Sprintf("/category-groups/%s", g.ID), token, nil)
	assert.Equal(t, http.StatusNotFound, wGet.Code)
}

func TestCategoryGroupHandler_TenantIsolation(t *testing.T) {
	truncateAll(t, testPool)
	_, tokenA := MustRegisterAndLogin(t, testRouter, "grp_iso_a")
	_, tokenB := MustRegisterAndLogin(t, testRouter, "grp_iso_b")
	gA := createCategoryGroup(t, tokenA, "Health", "#1F6B5C")

	w := Do(t, testRouter, "GET", fmt.Sprintf("/category-groups/%s", gA), tokenB, nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
	wUpd := Do(t, testRouter, "PUT", fmt.Sprintf("/category-groups/%s", gA), tokenB, map[string]any{"name": "x"})
	assert.Equal(t, http.StatusNotFound, wUpd.Code)

	// B can't file an Area under A's group, on create or update.
	wCat := Do(t, testRouter, "POST", "/categories", tokenB, map[string]any{
		"name": "Running", "color": "#000000", "group_id": gA,
	})
	assert.Equal(t, http.StatusBadRequest, wCat.Code, wCat.Body.String())
	catB := createCategory(t, tokenB, "Running", 0)
	wMove := Do(t, testRouter, "PUT", fmt.Sprintf("/categories/%s", catB), tokenB, map[string]any{"group_id": gA})
	assert.Equal(t, http.StatusBadRequest, wMove.Code, wMove.Body.String())

	// B's delete of A's group is a no-op.
	Do(t, testRouter, "DELETE", fmt.Sprintf("/category-groups/%s", gA), tokenB, nil)
	wStill := Do(t, testRouter, "GET", fmt.Sprintf("/category-groups/%s", gA), tokenA, nil)
	assert.Equal(t, http.StatusOK, wStill.Code)
}

func TestCategoryHandler_GroupMembership(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "cat_grp")
	lang := createCategoryGroup(t, token, "Language learning", "#2A56C6")
	health := createCategoryGroup(t, token, "Health", "#1F6B5C")

	w := Do(t, testRouter, "POST", "/categories", token, map[string]any{
		"name": "French", "color": "#4285F4", "group_id": lang,
	})
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	fr := decodeCategory(t, w.Body.Bytes())
	require.NotNil(t, fr.GroupID)
	assert.Equal(t, lang, *fr.GroupID)

	// Absent group_id keeps the group.
	wName := Do(t, testRouter, "PUT", fmt.Sprintf("/categories/%s", fr.ID), token, map[string]any{"name": "Français"})
	require.Equal(t, http.StatusOK, wName.Code, wName.Body.String())
	require.NotNil(t, decodeCategory(t, wName.Body.Bytes()).GroupID)

	// Move to another group.
	wMove := Do(t, testRouter, "PUT", fmt.Sprintf("/categories/%s", fr.ID), token, map[string]any{"group_id": health})
	require.Equal(t, http.StatusOK, wMove.Code, wMove.Body.String())
	assert.Equal(t, health, *decodeCategory(t, wMove.Body.Bytes()).GroupID)

	// Explicit null ungroups.
	wNull := Do(t, testRouter, "PUT", fmt.Sprintf("/categories/%s", fr.ID), token, map[string]any{"group_id": nil})
	require.Equal(t, http.StatusOK, wNull.Code, wNull.Body.String())
	assert.Nil(t, decodeCategory(t, wNull.Body.Bytes()).GroupID)

	// Unknown group → 400, not a 500 from the FK.
	wUnknown := Do(t, testRouter, "PUT", fmt.Sprintf("/categories/%s", fr.ID), token, map[string]any{"group_id": uuid.New()})
	assert.Equal(t, http.StatusBadRequest, wUnknown.Code, wUnknown.Body.String())

	// Deleting a group leaves its Areas in place, ungrouped.
	Do(t, testRouter, "PUT", fmt.Sprintf("/categories/%s", fr.ID), token, map[string]any{"group_id": lang})
	wDel := Do(t, testRouter, "DELETE", fmt.Sprintf("/category-groups/%s", lang), token, nil)
	require.Equal(t, http.StatusNoContent, wDel.Code)
	wGet := Do(t, testRouter, "GET", fmt.Sprintf("/categories/%s", fr.ID), token, nil)
	require.Equal(t, http.StatusOK, wGet.Code)
	assert.Nil(t, decodeCategory(t, wGet.Body.Bytes()).GroupID)
}

func TestCategoryHandler_Codes(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "cat_code")

	create := func(body map[string]any) (int, model.Category, string) {
		body["color"] = "#4285F4"
		w := Do(t, testRouter, "POST", "/categories", token, body)
		if w.Code != http.StatusCreated {
			return w.Code, model.Category{}, w.Body.String()
		}
		return w.Code, decodeCategory(t, w.Body.Bytes()), ""
	}

	// Derived from the name, skipping codes already taken.
	_, ca, _ := create(map[string]any{"name": "Calendar app"})
	assert.Equal(t, "CA", ca.Code)
	_, cl, _ := create(map[string]any{"name": "Climbing"})
	assert.Equal(t, "CL", cl.Code)

	// Explicit codes are trimmed and upper-cased.
	_, ja, _ := create(map[string]any{"name": "Japanese", "code": " jp "})
	assert.Equal(t, "JP", ja.Code)

	// Invalid explicit code → 400.
	code, _, body := create(map[string]any{"name": "Blog", "code": "TOOLONG"})
	assert.Equal(t, http.StatusBadRequest, code, body)
	code, _, body = create(map[string]any{"name": "Blog", "code": "B-1"})
	assert.Equal(t, http.StatusBadRequest, code, body)

	// Explicit code already used → 409 naming the code.
	code, _, body = create(map[string]any{"name": "Blog", "code": "ca"})
	assert.Equal(t, http.StatusConflict, code, body)
	assert.Contains(t, body, "code")

	// Update: rename the code, reject a taken one; an empty one is derived again.
	wUpd := Do(t, testRouter, "PUT", fmt.Sprintf("/categories/%s", cl.ID), token, map[string]any{"code": "cb"})
	require.Equal(t, http.StatusOK, wUpd.Code, wUpd.Body.String())
	assert.Equal(t, "CB", decodeCategory(t, wUpd.Body.Bytes()).Code)
	wTaken := Do(t, testRouter, "PUT", fmt.Sprintf("/categories/%s", cl.ID), token, map[string]any{"code": "CA"})
	assert.Equal(t, http.StatusConflict, wTaken.Code, wTaken.Body.String())
	wEmpty := Do(t, testRouter, "PUT", fmt.Sprintf("/categories/%s", cl.ID), token, map[string]any{"code": " "})
	require.Equal(t, http.StatusOK, wEmpty.Code, wEmpty.Body.String())
	assert.Equal(t, "CL", decodeCategory(t, wEmpty.Body.Bytes()).Code)

	// Renaming with an empty code derives it from the new name; the Area's own
	// current code doesn't count as taken.
	wRe := Do(t, testRouter, "PUT", fmt.Sprintf("/categories/%s", cl.ID), token, map[string]any{"name": "Cooking", "code": ""})
	require.Equal(t, http.StatusOK, wRe.Code, wRe.Body.String())
	assert.Equal(t, "CO", decodeCategory(t, wRe.Body.Bytes()).Code)
	wSame := Do(t, testRouter, "PUT", fmt.Sprintf("/categories/%s", cl.ID), token, map[string]any{"code": ""})
	require.Equal(t, http.StatusOK, wSame.Code, wSame.Body.String())
	assert.Equal(t, "CO", decodeCategory(t, wSame.Body.Bytes()).Code)

	// Codes are unique per owner, not globally.
	_, token2 := MustRegisterAndLogin(t, testRouter, "cat_code_2")
	w2 := Do(t, testRouter, "POST", "/categories", token2, map[string]any{"name": "Calendar app", "color": "#4285F4"})
	require.Equal(t, http.StatusCreated, w2.Code, w2.Body.String())
	assert.Equal(t, "CA", decodeCategory(t, w2.Body.Bytes()).Code)
}

func TestEventStats_IncludesGroup(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "stats_grp")
	lang := createCategoryGroup(t, token, "Language learning", "#2A56C6")
	w := Do(t, testRouter, "POST", "/categories", token, map[string]any{
		"name": "French", "color": "#4285F4", "group_id": lang,
	})
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	fr := decodeCategory(t, w.Body.Bytes())
	running := createCategory(t, token, "Running", 0) // ungrouped

	start := time.Date(2024, 6, 11, 9, 0, 0, 0, time.UTC)
	for _, cat := range []uuid.UUID{fr.ID, running} {
		wE := Do(t, testRouter, "POST", "/events", token, map[string]any{
			"title": "session", "start_time": start, "end_time": start.Add(time.Hour), "category_id": cat,
		})
		require.Equal(t, http.StatusCreated, wE.Code, wE.Body.String())
	}

	url := fmt.Sprintf("/events/stats?from=%s&to=%s",
		start.AddDate(0, 0, -1).Format(time.RFC3339), start.AddDate(0, 0, 1).Format(time.RFC3339))
	wS := Do(t, testRouter, "GET", url, token, nil)
	require.Equal(t, http.StatusOK, wS.Code, wS.Body.String())
	var stats model.TimeStats
	require.NoError(t, json.NewDecoder(wS.Body).Decode(&stats))
	require.Len(t, stats.Areas, 2)

	byName := map[string]model.AreaStat{}
	for _, a := range stats.Areas {
		byName[a.AreaName] = a
	}
	frStat := byName["French"]
	require.NotNil(t, frStat.GroupID)
	assert.Equal(t, lang, *frStat.GroupID)
	assert.Equal(t, "Language learning", frStat.GroupName)
	assert.Equal(t, "#2A56C6", frStat.GroupColor)
	assert.Equal(t, "FR", frStat.AreaCode)
	assert.Equal(t, 60, frStat.TotalMinutes)

	runStat := byName["Running"]
	assert.Nil(t, runStat.GroupID)
	assert.Empty(t, runStat.GroupName)
	assert.Equal(t, 60, runStat.TotalMinutes)
}

// Name and color are required on create; an update can't blank them either.
func TestCategoryAndGroup_RejectBlankFields(t *testing.T) {
	truncateAll(t, testPool)
	_, token := MustRegisterAndLogin(t, testRouter, "blank_fields")
	cat := createCategory(t, token, "Running", 0)
	grp := createCategoryGroup(t, token, "Health", "#1F6B5C")

	for _, tc := range []struct {
		method, path string
		body         map[string]any
	}{
		{"POST", "/categories", map[string]any{"name": "  ", "color": "#000000"}},
		{"POST", "/categories", map[string]any{"name": "Chess", "color": " "}},
		{"PUT", "/categories/" + cat.String(), map[string]any{"name": ""}},
		{"PUT", "/categories/" + cat.String(), map[string]any{"color": "  "}},
		{"POST", "/category-groups", map[string]any{"name": " ", "color": "#000000"}},
		{"PUT", "/category-groups/" + grp.String(), map[string]any{"name": ""}},
		{"PUT", "/category-groups/" + grp.String(), map[string]any{"color": ""}},
	} {
		w := Do(t, testRouter, tc.method, tc.path, token, tc.body)
		assert.Equal(t, http.StatusBadRequest, w.Code, "%s %s %v: %s", tc.method, tc.path, tc.body, w.Body.String())
	}

	// Untouched by the rejected updates.
	wC := Do(t, testRouter, "GET", "/categories/"+cat.String(), token, nil)
	assert.Equal(t, "Running", decodeCategory(t, wC.Body.Bytes()).Name)
	wG := Do(t, testRouter, "GET", "/category-groups/"+grp.String(), token, nil)
	var g model.CategoryGroup
	require.NoError(t, json.NewDecoder(wG.Body).Decode(&g))
	assert.Equal(t, "Health", g.Name)
	assert.Equal(t, "#1F6B5C", g.Color)
}
