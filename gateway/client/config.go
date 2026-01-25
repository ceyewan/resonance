package client

import (
	"time"

	"github.com/ceyewan/genesis/breaker"
	"github.com/ceyewan/genesis/ratelimit"
)

// 预留的治理配置，用于后续接入熔断与限流组件。
var (
	defaultBreakerConfig = &breaker.Config{
		MaxRequests:     5,
		Interval:        60 * time.Second,
		Timeout:         30 * time.Second,
		FailureRatio:    0.6,
		MinimumRequests: 10,
	}

	defaultServiceRateLimits = map[string]ratelimit.Limit{
		"logic.v1.AuthService": {
			Rate:  100,
			Burst: 200,
		},
		"logic.v1.SessionService": {
			Rate:  500,
			Burst: 1000,
		},
		"logic.v1.ChatService": {
			Rate:  5000,
			Burst: 10000,
		},
		"logic.v1.PresenceService": {
			Rate:  200,
			Burst: 500,
		},
	}

	defaultLimiterConfig = ratelimit.Limit{
		Rate:  500,
		Burst: 1000,
	}
)
