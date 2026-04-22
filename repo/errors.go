package repo

import "errors"

var (
	ErrUserNotFound           = errors.New("user not found")
	ErrSessionNotFound        = errors.New("session not found")
	ErrSessionMemberNotFound  = errors.New("session member not found")
	ErrRouteNotFound          = errors.New("route not found")
	ErrMessageNotFound        = errors.New("message not found")
	ErrMessageAlreadyRecalled = errors.New("message already recalled")
)
