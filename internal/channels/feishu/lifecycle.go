package feishuchannel

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	channelcore "goclaw/internal/channels"
)

type runtimeBinding struct {
	params channelcore.BuildRuntimeParams
}

type managedLongpollRuntime struct {
	account resolvedAccount
	cancel  context.CancelFunc
	done    chan struct{}
}

func (c *Channel) BindRuntime(params channelcore.BuildRuntimeParams) {
	if c == nil {
		return
	}

	c.runtimeMu.Lock()
	defer c.runtimeMu.Unlock()
	c.runtimeBinding = &runtimeBinding{params: params}
	if c.longpollRuntimes == nil {
		c.longpollRuntimes = make(map[string]*managedLongpollRuntime)
	}
}

func (c *Channel) StartAccount(ctx context.Context, account channelcore.AccountRef) error {
	resolved, binding, err := c.resolveManagedLongpollAccount(account)
	if err != nil {
		return err
	}

	c.runtimeMu.Lock()
	if existing, ok := c.longpollRuntimes[resolved.AccountID]; ok && existing != nil {
		c.runtimeMu.Unlock()
		return nil
	}

	runCtx, cancel := context.WithCancel(binding.params.RootContext)
	runtime := &managedLongpollRuntime{
		account: resolved,
		cancel:  cancel,
		done:    make(chan struct{}),
	}
	c.longpollRuntimes[resolved.AccountID] = runtime
	c.runtimeMu.Unlock()

	go c.runManagedLongpoll(runtimeBinding{
		params: binding.params,
	}, runtime, runCtx)
	return nil
}

func (c *Channel) StopAccount(ctx context.Context, account channelcore.AccountRef) error {
	resolved, _, err := c.resolveManagedLongpollAccount(account)
	if err != nil {
		return err
	}

	c.runtimeMu.Lock()
	runtime := c.longpollRuntimes[resolved.AccountID]
	c.runtimeMu.Unlock()
	if runtime == nil {
		return nil
	}

	runtime.cancel()

	select {
	case <-runtime.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Channel) RestartAccount(ctx context.Context, account channelcore.AccountRef) error {
	if err := c.StopAccount(ctx, account); err != nil {
		return err
	}
	return c.StartAccount(ctx, account)
}

func (c *Channel) longpollLifecycleAvailable() bool {
	if c == nil {
		return false
	}

	c.runtimeMu.Lock()
	defer c.runtimeMu.Unlock()
	return c.longpollLifecycleAvailableLocked()
}

func (c *Channel) longpollLifecycleAvailableLocked() bool {
	if c.runtimeBinding == nil {
		return false
	}
	return c.runtimeBinding.params.RootContext != nil &&
		c.runtimeBinding.params.ManagedRuntimeObserver != nil
}

func (c *Channel) resolveManagedLongpollAccount(
	account channelcore.AccountRef,
) (resolvedAccount, *runtimeBinding, error) {
	if c == nil {
		return resolvedAccount{}, nil, fmt.Errorf("%w: feishu channel is nil", channelcore.ErrAccountLifecycleUnsupported)
	}

	c.runtimeMu.Lock()
	binding := c.runtimeBinding
	available := c.longpollLifecycleAvailableLocked()
	c.runtimeMu.Unlock()
	if !available || binding == nil {
		return resolvedAccount{}, nil, fmt.Errorf(
			"%w: feishu longpoll lifecycle host is not attached",
			channelcore.ErrAccountLifecycleUnsupported,
		)
	}

	resolved, ok := c.findAccount(account)
	if !ok || !isLongpollMode(resolved.Settings.ConnectionMode) {
		return resolvedAccount{}, nil, fmt.Errorf(
			"%w: feishu account %q is not a managed longpoll account",
			channelcore.ErrAccountLifecycleUnsupported,
			strings.TrimSpace(account.AccountID),
		)
	}
	if !resolved.Enabled || !resolved.Configured {
		return resolvedAccount{}, nil, fmt.Errorf(
			"%w: feishu account %q is not enabled/configured",
			channelcore.ErrAccountLifecycleUnsupported,
			resolved.AccountID,
		)
	}
	return resolved, binding, nil
}

func (c *Channel) findAccount(account channelcore.AccountRef) (resolvedAccount, bool) {
	if c == nil {
		return resolvedAccount{}, false
	}

	accountID := strings.TrimSpace(account.AccountID)
	profileID := strings.TrimSpace(string(account.ProfileID))
	for _, candidate := range c.accounts {
		if accountID != "" && candidate.AccountID != accountID {
			continue
		}
		if profileID != "" && strings.TrimSpace(string(candidate.Settings.ProfileID)) != profileID {
			continue
		}
		return candidate, true
	}
	return resolvedAccount{}, false
}

func (c *Channel) runManagedLongpoll(
	binding runtimeBinding,
	runtime *managedLongpollRuntime,
	ctx context.Context,
) {
	defer func() {
		c.runtimeMu.Lock()
		delete(c.longpollRuntimes, runtime.account.AccountID)
		c.runtimeMu.Unlock()
		close(runtime.done)
	}()

	restartCount := 0
	for {
		now := time.Now().UTC()
		binding.params.ManagedRuntimeObserver.MarkRuntimeStarting(runtime.account.TransportID, now)

		runner, err := c.buildLongpollRunner(runtime.account, binding.params)
		if err != nil {
			stoppedAt := time.Now().UTC()
			binding.params.ManagedRuntimeObserver.MarkRuntimeStopped(runtime.account.TransportID, stoppedAt, err)
			binding.params.ManagedRuntimeObserver.ReportRuntimeFailure(runtime.account.TransportID, err)
			return
		}

		err = runner.Run(ctx)
		stoppedAt := time.Now().UTC()
		if err == nil || errors.Is(err, context.Canceled) || ctx.Err() != nil {
			binding.params.ManagedRuntimeObserver.MarkRuntimeStopped(runtime.account.TransportID, stoppedAt, nil)
			return
		}

		if !shouldRetryManagedLongpoll(binding.params.ManagedRuntimePolicy, restartCount) {
			binding.params.ManagedRuntimeObserver.MarkRuntimeStopped(runtime.account.TransportID, stoppedAt, err)
			binding.params.ManagedRuntimeObserver.ReportRuntimeFailure(runtime.account.TransportID, err)
			return
		}

		backoff := managedLongpollBackoff(binding.params.ManagedRuntimePolicy, restartCount)
		nextRetryAt := stoppedAt.Add(backoff)
		binding.params.ManagedRuntimeObserver.MarkRuntimeBackingOff(runtime.account.TransportID, stoppedAt, nextRetryAt, err)

		if waitErr := waitForManagedLongpollRetry(ctx, backoff); waitErr != nil {
			binding.params.ManagedRuntimeObserver.MarkRuntimeStopped(runtime.account.TransportID, time.Now().UTC(), nil)
			return
		}
		restartCount++
	}
}

func shouldRetryManagedLongpoll(policy channelcore.ManagedRuntimePolicy, restartCount int) bool {
	return policy.Enabled && policy.MaxAttempts > 0 && restartCount < policy.MaxAttempts
}

func managedLongpollBackoff(policy channelcore.ManagedRuntimePolicy, restartCount int) time.Duration {
	initial := policy.InitialBackoff
	maxBackoff := policy.MaxBackoff
	if initial <= 0 {
		initial = time.Second
	}
	if maxBackoff <= 0 {
		maxBackoff = initial
	}

	backoff := initial
	for i := 0; i < restartCount; i++ {
		backoff *= 2
		if backoff >= maxBackoff {
			return maxBackoff
		}
	}
	if backoff > maxBackoff {
		return maxBackoff
	}
	return backoff
}

func waitForManagedLongpollRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

var _ channelcore.RuntimeBinder = (*Channel)(nil)
var _ channelcore.AccountLifecycleController = (*Channel)(nil)
