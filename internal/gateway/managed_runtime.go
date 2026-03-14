package gateway

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	channelcore "goclaw/internal/channels"
)

type managedRuntimeObserver struct {
	manager   *ChannelManager
	inventory *TransportInventory
	cancel    context.CancelFunc
	errCh     chan error
}

func (o managedRuntimeObserver) MarkRuntimeStarting(runtimeID string, now time.Time) {
	if o.manager != nil {
		o.manager.MarkRuntimeStarting(runtimeID, now)
	}
	if o.inventory != nil {
		o.inventory.MarkStarting(runtimeID, now)
	}
}

func (o managedRuntimeObserver) MarkRuntimeBackingOff(
	runtimeID string,
	now time.Time,
	nextRetryAt time.Time,
	err error,
) {
	if o.manager != nil {
		o.manager.MarkRuntimeBackingOff(runtimeID, now, nextRetryAt, err)
	}
	if o.inventory != nil {
		o.inventory.MarkBackingOff(runtimeID, now, nextRetryAt, err)
	}
}

func (o managedRuntimeObserver) MarkRuntimeStopped(runtimeID string, now time.Time, err error) {
	if o.manager != nil {
		o.manager.MarkRuntimeStopped(runtimeID, now, err)
	}
	if o.inventory != nil {
		o.inventory.MarkStopped(runtimeID, now, err)
	}
}

func (o managedRuntimeObserver) ReportRuntimeFailure(runtimeID string, err error) {
	if err == nil || o.errCh == nil {
		return
	}
	wrapped := fmt.Errorf("%s: %w", runtimeID, err)
	select {
	case o.errCh <- wrapped:
	default:
	}
	if o.cancel != nil {
		o.cancel()
	}
}

func managedRuntimePolicyFromRestart(policy RestartPolicy) channelcore.ManagedRuntimePolicy {
	return channelcore.ManagedRuntimePolicy{
		Enabled:        policy.Enabled,
		MaxAttempts:    policy.MaxAttempts,
		InitialBackoff: policy.InitialBackoff,
		MaxBackoff:     policy.MaxBackoff,
	}
}

func (s *Server) startManagedAccounts(ctx context.Context) (int, error) {
	if s == nil || s.App == nil || s.App.Channels == nil {
		return 0, nil
	}

	started := 0
	for _, spec := range s.App.Channels.AccountLifecycleSpecs() {
		if !spec.OperatorManaged || !spec.Enabled || !spec.Configured || !spec.SupportsStart {
			continue
		}
		if err := s.App.Channels.StartAccount(ctx, channelcore.AccountRef{
			Provider:  spec.Provider,
			AccountID: spec.AccountID,
			ProfileID: spec.ProfileID,
		}); err != nil {
			return started, err
		}
		started++
	}
	return started, nil
}

func runGatewayTransports(
	ctx context.Context,
	cancel context.CancelFunc,
	transports []channelcore.Transport,
	errCh chan error,
) error {
	if len(transports) == 0 {
		select {
		case err := <-errCh:
			return err
		case <-ctx.Done():
			if err := ctx.Err(); err != nil && !errors.Is(err, context.Canceled) {
				return err
			}
			select {
			case err := <-errCh:
				return err
			default:
				return nil
			}
		}
	}

	doneCh := make(chan struct{})
	var wg sync.WaitGroup
	for _, transport := range transports {
		if transport == nil {
			continue
		}
		wg.Add(1)
		go func(transport channelcore.Transport) {
			defer wg.Done()
			if err := transport.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
				select {
				case errCh <- fmt.Errorf("%s: %w", transport.Name(), err):
				default:
				}
				cancel()
			}
		}(transport)
	}

	go func() {
		wg.Wait()
		close(doneCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		select {
		case err := <-errCh:
			return err
		default:
			return nil
		}
	case <-doneCh:
		select {
		case err := <-errCh:
			return err
		default:
			return nil
		}
	}
}
