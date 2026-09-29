package model

import (
	"strconv"
	"strings"
	"unicode"
)

// MaxCategoryCodeLen bounds an Area's short code so it fits a small badge.
const MaxCategoryCodeLen = 4

// NormalizeCategoryCode trims and upper-cases a caller-supplied code and
// reports whether the result is valid: 1..MaxCategoryCodeLen letters or digits.
func NormalizeCategoryCode(code string) (string, bool) {
	code = strings.ToUpper(strings.TrimSpace(code))
	runes := []rune(code)
	if len(runes) == 0 || len(runes) > MaxCategoryCodeLen {
		return code, false
	}
	for _, r := range runes {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return code, false
		}
	}
	return code, true
}

// DeriveCategoryCode picks a short code for an Area named name that is not in
// used (keys are upper-case codes). It tries the first letter paired with each
// later letter ("Climbing" → CL, CI, CM, …), then the first letter alone, then
// the first letter with a number (C2, C3, …). Names without letters use "X".
func DeriveCategoryCode(name string, used map[string]bool) string {
	var letters []rune
	for _, r := range name {
		if unicode.IsLetter(r) {
			letters = append(letters, unicode.ToUpper(r))
		}
	}
	first := 'X'
	if len(letters) > 0 {
		first = letters[0]
	}
	var candidates []string
	for _, r := range letters[min(1, len(letters)):] {
		candidates = append(candidates, string([]rune{first, r}))
	}
	candidates = append(candidates, string(first))
	for _, c := range candidates {
		if !used[c] {
			return c
		}
	}
	for n := 2; ; n++ {
		c := string(first) + strconv.Itoa(n)
		if !used[c] {
			return c
		}
	}
}
