package longpoll

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrMissingProfileID = errors.New("feishu longpoll: missing profile id")
	ErrMissingAppID     = errors.New("feishu longpoll: missing app id")
	ErrMissingAppSecret = errors.New("feishu longpoll: missing app secret")
)

const transientReconnectBackoff = 2 * time.Second

type Runner struct {
	params Params
	state  *stateStore
}

func New(params Params) (*Runner, error) {
	params = params.withDefaults()

	switch {
	case strings.TrimSpace(string(params.ProfileID)) == "":
		return nil, ErrMissingProfileID
	case strings.TrimSpace(params.AppID) == "":
		return nil, ErrMissingAppID
	case strings.TrimSpace(params.AppSecret) == "":
		return nil, ErrMissingAppSecret
	case params.Ingestor == nil:
		return nil, ErrMissingMessageIngestor
	}

	return &Runner{
		params: params,
		state:  &stateStore{},
	}, nil
}

func (r *Runner) State() State {
	return r.state.snapshot()
}

func (r *Runner) Run(ctx context.Context) error {
	r.params.Logger.Info(
		"feishu longpoll: starting runner",
		"profile_id", r.params.ProfileID,
		"base_url", resolveWSBaseURL(r.params.BaseURL),
	)

	dispatcher, err := newEventDispatcher(
		r.params.ProfileID,
		r.params.Ingestor,
		r.params.EventObserver,
		r.params.Logger,
		r.state.setLastEventAt,
	)
	if err != nil {
		return err
	}

	r.state.setRunning(true)
	defer r.state.setRunning(false)

	for attempt := 1; ; attempt++ {
		client := r.params.clientFactory(r.params, dispatcher.build())
		errCh := make(chan error, 1)
		go func() {
			errCh <- client.Start(ctx)
		}()

		select {
		case <-ctx.Done():
			r.params.Logger.Info("feishu longpoll: stopping runner", "reason", "context_done")
			return nil
		case err := <-errCh:
			r.state.setError(err)
			if err == nil {
				r.params.Logger.Info("feishu longpoll: runner exited")
				return nil
			}
			if !isTransientDisconnect(err) {
				r.params.Logger.Error("feishu longpoll: runner exited with error", "error", err)
				return fmt.Errorf("run feishu longpoll: %w", err)
			}

			r.params.Logger.Warn(
				"feishu longpoll: transient disconnect, reconnecting",
				"attempt", attempt,
				"backoff", transientReconnectBackoff,
				"error", err,
			)
			if waitErr := r.params.waitBackoff(ctx, transientReconnectBackoff); waitErr != nil {
				if errors.Is(waitErr, context.Canceled) {
					r.params.Logger.Info("feishu longpoll: stopping runner", "reason", "context_done")
					return nil
				}
				return fmt.Errorf("run feishu longpoll: wait reconnect backoff: %w", waitErr)
			}
		}
	}
}

func isTransientDisconnect(err error) bool {
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	if message == "" {
		return false
	}
	return strings.Contains(message, "close 1006") ||
		strings.Contains(message, "unexpected eof") ||
		strings.Contains(message, "websocket: close 1001") ||
		strings.Contains(message, "connection reset by peer") ||
		strings.Contains(message, "broken pipe")
}
