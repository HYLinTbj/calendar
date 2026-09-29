package model_test

import (
	"testing"

	"github.com/hylin/calendar/internal/model"
	"github.com/stretchr/testify/assert"
)

func TestDeriveCategoryCode(t *testing.T) {
	cases := []struct {
		name string
		used []string
		want string
	}{
		{"French", nil, "FR"},
		{"Calendar app", nil, "CA"},
		{"Climbing", []string{"CA"}, "CL"},
		{"Climbing", []string{"CL"}, "CI"},
		{"japanese", nil, "JA"},
		{"Été", nil, "ÉT"},
		{"C", nil, "C"},
		{"C", []string{"C"}, "C2"},
		{"Co", []string{"CO", "C", "C2"}, "C3"},
		{"123 !!", nil, "X"},
		{"", []string{"X"}, "X2"},
	}
	for _, tc := range cases {
		used := map[string]bool{}
		for _, u := range tc.used {
			used[u] = true
		}
		assert.Equal(t, tc.want, model.DeriveCategoryCode(tc.name, used), "name=%q used=%v", tc.name, tc.used)
	}
}

func TestNormalizeCategoryCode(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"fr", "FR", true},
		{"  ja ", "JA", true},
		{"C2", "C2", true},
		{"ABCD", "ABCD", true},
		{"ABCDE", "ABCDE", false},
		{"", "", false},
		{"   ", "", false},
		{"F-R", "F-R", false},
	}
	for _, tc := range cases {
		got, ok := model.NormalizeCategoryCode(tc.in)
		assert.Equal(t, tc.ok, ok, "in=%q", tc.in)
		if tc.ok {
			assert.Equal(t, tc.want, got, "in=%q", tc.in)
		}
	}
}
