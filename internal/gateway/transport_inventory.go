package gateway

import (
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"goclaw/internal/domain"
	"goclaw/internal/runtime"
)

type TransportSource string

const (
	TransportSourceChannel TransportSource = "channel"
	TransportSourceWorker  TransportSource = "worker"
)

type TransportKind string

const (
	TransportKindWebhook    TransportKind = "webhook"
	TransportKindBackground TransportKind = "background"
	TransportKindWorker     TransportKind = "worker"
)

type TransportState string

const (
	TransportStateIdle       TransportState = "idle"
	TransportStateRunning    TransportState = "running"
	TransportStateBackingOff TransportState = "backing_off"
	TransportStateFailed     TransportState = "failed"
)

type TransportBinding struct {
	Provider    domain.Provider  `json:"provider"`
	AccountID   string           `json:"account_id,omitempty"`
	ProfileID   domain.ProfileID `json:"profile_id,omitempty"`
	WebhookPath string           `json:"webhook_path,omitempty"`
}

type TransportStatus struct {
	Name               string             `json:"name"`
	Source             TransportSource    `json:"source"`
	Kind               TransportKind      `json:"kind"`
	Managed            bool               `json:"managed"`
	Address            string             `json:"address,omitempty"`
	Bindings           []TransportBinding `json:"bindings,omitempty"`
	RestartEnabled     bool               `json:"restart_enabled,omitempty"`
	MaxRestartAttempts int                `json:"max_restart_attempts,omitempty"`
	RestartCount       int                `json:"restart_count,omitempty"`
	State              TransportState     `json:"state"`
	Running            bool               `json:"running"`
	LastError          string             `json:"last_error,omitempty"`
	LastStartAt        *time.Time         `json:"last_start_at,omitempty"`
	LastStopAt         *time.Time         `json:"last_stop_at,omitempty"`
	LastFailureAt      *time.Time         `json:"last_failure_at,omitempty"`
	NextRetryAt        *time.Time         `json:"next_retry_at,omitempty"`
}

type TransportInventory struct {
	mu       sync.RWMutex
	statuses map[string]TransportStatus
}

func newTransportInventoryForApp(
	app *runtime.App,
	channelManager *ChannelManager,
	logger *slog.Logger,
) *TransportInventory {
	inventory := &TransportInventory{
		statuses: make(map[string]TransportStatus),
	}
	if channelManager != nil {
		inventory.seedChannelTransports(channelManager.Snapshot())
	}
	if app != nil {
		for _, transport := range buildWorkerTransports(app, logger) {
			if transport == nil {
				continue
			}
			inventory.statuses[strings.TrimSpace(transport.Name())] = TransportStatus{
				Name:    strings.TrimSpace(transport.Name()),
				Source:  TransportSourceWorker,
				Kind:    TransportKindWorker,
				Managed: false,
				State:   TransportStateIdle,
			}
		}
	}
	return inventory
}

func (i *TransportInventory) seedChannelTransports(channels []ChannelAccountStatus) {
	for _, channel := range channels {
		if !channel.Enabled || !channel.Configured {
			continue
		}
		name := strings.TrimSpace(channel.RuntimeID)
		if name == "" {
			continue
		}

		status := i.statuses[name]
		if strings.TrimSpace(status.Name) == "" {
			status = TransportStatus{
				Name:    name,
				Source:  TransportSourceChannel,
				Kind:    resolveTransportKind(channel),
				Managed: true,
				State:   transportStateFromChannelState(channel.State),
				Running: channel.Running,
				Address: strings.TrimSpace(channel.WebhookAddress),
			}
		}
		status.RestartEnabled = status.RestartEnabled || channel.RestartEnabled
		if channel.MaxRestartAttempts > status.MaxRestartAttempts {
			status.MaxRestartAttempts = channel.MaxRestartAttempts
		}
		if channel.RestartCount > status.RestartCount {
			status.RestartCount = channel.RestartCount
		}
		if status.Address == "" {
			status.Address = strings.TrimSpace(channel.WebhookAddress)
		}
		status = mergeTransportStatusWithChannel(status, channel)
		status.Bindings = append(status.Bindings, TransportBinding{
			Provider:    channel.Provider,
			AccountID:   strings.TrimSpace(channel.AccountID),
			ProfileID:   channel.ProfileID,
			WebhookPath: strings.TrimSpace(channel.WebhookPath),
		})
		i.statuses[name] = status
	}

	for key, status := range i.statuses {
		if status.Source != TransportSourceChannel {
			continue
		}
		slices.SortFunc(status.Bindings, compareTransportBindings)
		i.statuses[key] = status
	}
}

func compareTransportBindings(a, b TransportBinding) int {
	switch {
	case a.Provider != b.Provider:
		if a.Provider < b.Provider {
			return -1
		}
		return 1
	case a.AccountID != b.AccountID:
		if a.AccountID < b.AccountID {
			return -1
		}
		return 1
	case a.ProfileID != b.ProfileID:
		if a.ProfileID < b.ProfileID {
			return -1
		}
		return 1
	default:
		return 0
	}
}

func resolveTransportKind(channel ChannelAccountStatus) TransportKind {
	if strings.TrimSpace(channel.WebhookAddress) != "" {
		return TransportKindWebhook
	}
	return TransportKindBackground
}

func transportStateFromChannelState(state ChannelAccountState) TransportState {
	switch state {
	case ChannelAccountStateRunning:
		return TransportStateRunning
	case ChannelAccountStateBackingOff:
		return TransportStateBackingOff
	case ChannelAccountStateFailed:
		return TransportStateFailed
	default:
		return TransportStateIdle
	}
}

func mergeTransportStatusWithChannel(
	status TransportStatus,
	channel ChannelAccountStatus,
) TransportStatus {
	status.State = transportStateFromChannelState(channel.State)
	status.Running = channel.Running
	status.LastError = channel.LastError
	status.LastStartAt = cloneTimePointer(channel.LastStartAt)
	status.LastStopAt = cloneTimePointer(channel.LastStopAt)
	status.LastFailureAt = cloneTimePointer(channel.LastFailureAt)
	status.NextRetryAt = cloneTimePointer(channel.NextRetryAt)
	return status
}

func cloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := value.UTC()
	return &cloned
}

func (i *TransportInventory) Snapshot() []TransportStatus {
	if i == nil {
		return nil
	}

	i.mu.RLock()
	defer i.mu.RUnlock()

	snapshot := make([]TransportStatus, 0, len(i.statuses))
	for _, status := range i.statuses {
		snapshot = append(snapshot, cloneTransportStatus(status))
	}
	slices.SortFunc(snapshot, compareTransportStatuses)
	return snapshot
}

func cloneTransportStatus(status TransportStatus) TransportStatus {
	cloned := status
	cloned.Bindings = append([]TransportBinding(nil), status.Bindings...)
	cloned.LastStartAt = cloneTimePointer(status.LastStartAt)
	cloned.LastStopAt = cloneTimePointer(status.LastStopAt)
	cloned.LastFailureAt = cloneTimePointer(status.LastFailureAt)
	cloned.NextRetryAt = cloneTimePointer(status.NextRetryAt)
	return cloned
}

func compareTransportStatuses(a, b TransportStatus) int {
	if a.Name < b.Name {
		return -1
	}
	if a.Name > b.Name {
		return 1
	}
	return 0
}

func (i *TransportInventory) MarkStarting(name string, now time.Time) {
	i.updateTransport(name, func(status *TransportStatus) {
		startedAt := now.UTC()
		status.State = TransportStateRunning
		status.Running = true
		status.LastError = ""
		status.LastStartAt = &startedAt
		status.NextRetryAt = nil
	})
}

func (i *TransportInventory) MarkBackingOff(
	name string,
	now time.Time,
	nextRetryAt time.Time,
	err error,
) {
	i.updateTransport(name, func(status *TransportStatus) {
		stoppedAt := now.UTC()
		failureAt := now.UTC()
		retryAt := nextRetryAt.UTC()
		status.State = TransportStateBackingOff
		status.Running = false
		status.LastStopAt = &stoppedAt
		status.LastFailureAt = &failureAt
		status.NextRetryAt = &retryAt
		status.RestartCount++
		if err != nil {
			status.LastError = err.Error()
		}
	})
}

func (i *TransportInventory) MarkStopped(name string, now time.Time, err error) {
	i.updateTransport(name, func(status *TransportStatus) {
		stoppedAt := now.UTC()
		status.Running = false
		status.LastStopAt = &stoppedAt
		status.NextRetryAt = nil
		if err != nil {
			failureAt := now.UTC()
			status.State = TransportStateFailed
			status.LastError = err.Error()
			status.LastFailureAt = &failureAt
			return
		}
		status.State = TransportStateIdle
		status.LastError = ""
	})
}

func (i *TransportInventory) updateTransport(name string, mutate func(*TransportStatus)) {
	if i == nil || strings.TrimSpace(name) == "" || mutate == nil {
		return
	}

	i.mu.Lock()
	defer i.mu.Unlock()

	key := strings.TrimSpace(name)
	status := i.statuses[key]
	if strings.TrimSpace(status.Name) == "" {
		status = TransportStatus{
			Name:   key,
			Source: TransportSourceWorker,
			Kind:   TransportKindWorker,
			State:  TransportStateIdle,
		}
	}
	mutate(&status)
	i.statuses[key] = status
}
