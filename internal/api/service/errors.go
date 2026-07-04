package service

import "errors"

var (
	ErrValidation = errors.New("validation error")
	ErrInvalidID  = errors.New("invalid id")
)
