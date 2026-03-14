package gateway

import (
	"time"

	"goclaw/internal/config"
)

const (
	defaultInitialRestartBackoff = time.Second
	defaultMaxRestartBackoff     = 30 * time.Second
)

type RestartPolicy struct {
	Enabled        bool
	MaxAttempts    int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

func newRestartPolicy(cfg config.GatewayRestartConfig) RestartPolicy {
	policy := RestartPolicy{
		Enabled:        cfg.Enabled,
		MaxAttempts:    cfg.MaxAttempts,
		InitialBackoff: time.Duration(cfg.InitialBackoffSeconds) * time.Second,
		MaxBackoff:     time.Duration(cfg.MaxBackoffSeconds) * time.Second,
	}
	if policy.InitialBackoff <= 0 {
		policy.InitialBackoff = defaultInitialRestartBackoff
	}
	if policy.MaxBackoff <= 0 {
		policy.MaxBackoff = defaultMaxRestartBackoff
	}
	if policy.MaxBackoff < policy.InitialBackoff {
		policy.MaxBackoff = policy.InitialBackoff
	}
	return policy
}

func (p RestartPolicy) ShouldRetry(restartCount int) bool {
	return p.Enabled && p.MaxAttempts > 0 && restartCount < p.MaxAttempts
}

func (p RestartPolicy) Backoff(restartCount int) time.Duration {
	backoff := p.InitialBackoff
	for i := 0; i < restartCount; i++ {
		if backoff >= p.MaxBackoff {
			return p.MaxBackoff
		}
		backoff *= 2
		if backoff >= p.MaxBackoff {
			return p.MaxBackoff
		}
	}
	if backoff <= 0 {
		return defaultInitialRestartBackoff
	}
	return backoff
}
