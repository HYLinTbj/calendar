package repository

import "errors"

var (
	ErrDeleteDefault = errors.New("cannot delete the default calendar")
	ErrSelfShare     = errors.New("cannot share a calendar with yourself")
)
