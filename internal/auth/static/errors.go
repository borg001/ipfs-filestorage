package static

import "errors"

var (
	errNoToken      = errors.New("no token provided")
	errInvalidToken = errors.New("invalid token")
)
