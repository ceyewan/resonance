package repo

import "errors"

var (
	ErrUserNotFound                    = errors.New("user not found")
	ErrTenantMembershipNotFound        = errors.New("tenant membership not found")
	ErrIdentityVersionConflict         = errors.New("identity version conflict")
	ErrUnknownSystemRole               = errors.New("unknown system role")
	ErrSessionNotFound                 = errors.New("session not found")
	ErrSessionMemberNotFound           = errors.New("session member not found")
	ErrSessionCreateConflict           = errors.New("session idempotency key conflicts with persisted session")
	ErrRouteNotFound                   = errors.New("route not found")
	ErrMessageNotFound                 = errors.New("message not found")
	ErrMessageAlreadyRecalled          = errors.New("message already recalled")
	ErrMessageIdempotencyConflict      = errors.New("message idempotency key reused with different payload")
	ErrAgentFinalMessageNotCommittable = errors.New("agent final message is not committable")
	ErrAgentRunNotFound                = errors.New("agent run not found")
	ErrAgentRunSourceConflict          = errors.New("agent run source event reused with different payload")
	ErrNoAgentRunAvailable             = errors.New("no agent run available")
	ErrAgentRunFenceLost               = errors.New("agent run lease or version fence lost")
	ErrAgentRunInvalidTransition       = errors.New("invalid agent run state transition")
	ErrAgentRunCommitConflict          = errors.New("agent run final commit result conflicts with persisted result")
	ErrAgentSessionBindingNotFound     = errors.New("agent session binding not found")
	ErrAgentSessionBindingConflict     = errors.New("agent session binding compare-and-swap conflict")
)
