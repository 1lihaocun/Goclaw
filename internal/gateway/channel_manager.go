package gateway

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	channelcore "goclaw/internal/channels"
	"goclaw/internal/domain"
)

type ChannelAccountState string

const (
	ChannelAccountStateDisabled      ChannelAccountState = "disabled"
	ChannelAccountStateNotConfigured ChannelAccountState = "not_configured"
	ChannelAccountStateIdle          ChannelAccountState = "idle"
	ChannelAccountStateRunning       ChannelAccountState = "running"
	ChannelAccountStateBackingOff    ChannelAccountState = "backing_off"
	ChannelAccountStateFailed        ChannelAccountState = "failed"
)

type ChannelAccountStatus struct {
	Provider           domain.Provider     `json:"provider"`
	AccountID          string              `json:"account_id"`
	Name               string              `json:"name,omitempty"`
	ProfileID          domain.ProfileID    `json:"profile_id"`
	Enabled            bool                `json:"enabled"`
	Configured         bool                `json:"configured"`
	ConnectionMode     string              `json:"connection_mode"`
	TransportID        string              `json:"transport_id,omitempty"`
	RuntimeID          string              `json:"runtime_id,omitempty"`
	WebhookAddress     string              `json:"webhook_address,omitempty"`
	WebhookPath        string              `json:"webhook_path,omitempty"`
	BackgroundOnly     bool                `json:"background_only,omitempty"`
	SupportsIngress    bool                `json:"supports_ingress,omitempty"`
	RestartEnabled     bool                `json:"restart_enabled,omitempty"`
	MaxRestartAttempts int                 `json:"max_restart_attempts,omitempty"`
	RestartCount       int                 `json:"restart_count,omitempty"`
	State              ChannelAccountState `json:"state"`
	Running            bool                `json:"running"`
	LastError          string              `json:"last_error,omitempty"`
	LastStartAt        *time.Time          `json:"last_start_at,omitempty"`
	LastStopAt         *time.Time          `json:"last_stop_at,omitempty"`
	LastFailureAt      *time.Time          `json:"last_failure_at,omitempty"`
	NextRetryAt        *time.Time          `json:"next_retry_at,omitempty"`
}

type ChannelManager struct {
	mu         sync.RWMutex
	statuses   map[string]ChannelAccountStatus
	runtimeIDs map[string][]string
}

func NewChannelManager(registry *channelcore.Registry) *ChannelManager {
	manager := &ChannelManager{
		statuses:   make(map[string]ChannelAccountStatus),
		runtimeIDs: make(map[string][]string),
	}
	if registry == nil {
		return manager
	}

	for _, snapshot := range registry.AccountSnapshots() {
		status := ChannelAccountStatus{
			Provider:        snapshot.Provider,
			AccountID:       strings.TrimSpace(snapshot.AccountID),
			Name:            strings.TrimSpace(snapshot.Name),
			ProfileID:       snapshot.ProfileID,
			Enabled:         snapshot.Enabled,
			Configured:      snapshot.Configured,
			ConnectionMode:  strings.TrimSpace(snapshot.ConnectionMode),
			TransportID:     strings.TrimSpace(snapshot.TransportID),
			RuntimeID:       strings.TrimSpace(snapshot.RuntimeID),
			WebhookAddress:  strings.TrimSpace(snapshot.WebhookAddress),
			WebhookPath:     strings.TrimSpace(snapshot.WebhookPath),
			BackgroundOnly:  snapshot.BackgroundOnly,
			SupportsIngress: snapshot.SupportsIngress,
			State:           resolveInitialChannelAccountState(snapshot),
		}
		key := buildChannelAccountStatusKey(status.Provider, status.AccountID, status.ProfileID)
		manager.statuses[key] = status
		if status.RuntimeID != "" {
			manager.runtimeIDs[status.RuntimeID] = append(manager.runtimeIDs[status.RuntimeID], key)
		}
	}
	return manager
}

func resolveInitialChannelAccountState(snapshot channelcore.AccountSnapshot) ChannelAccountState {
	switch {
	case !snapshot.Enabled:
		return ChannelAccountStateDisabled
	case !snapshot.Configured:
		return ChannelAccountStateNotConfigured
	default:
		return ChannelAccountStateIdle
	}
}

func (m *ChannelManager) ApplyRestartPolicy(policy RestartPolicy) {
	if m == nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for key, status := range m.statuses {
		status.RestartEnabled = policy.Enabled && policy.MaxAttempts > 0
		if status.RestartEnabled {
			status.MaxRestartAttempts = policy.MaxAttempts
		} else {
			status.MaxRestartAttempts = 0
		}
		m.statuses[key] = status
	}
}

func buildChannelAccountStatusKey(
	provider domain.Provider,
	accountID string,
	profileID domain.ProfileID,
) string {
	return string(provider) + ":" + strings.TrimSpace(accountID) + ":" + strings.TrimSpace(string(profileID))
}

func (m *ChannelManager) Snapshot() []ChannelAccountStatus {
	if m == nil {
		return nil
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	snapshots := make([]ChannelAccountStatus, 0, len(m.statuses))
	for _, status := range m.statuses {
		snapshots = append(snapshots, cloneChannelAccountStatus(status))
	}
	slices.SortFunc(snapshots, compareChannelAccountStatuses)
	return snapshots
}

func cloneChannelAccountStatus(status ChannelAccountStatus) ChannelAccountStatus {
	cloned := status
	if status.LastStartAt != nil {
		startedAt := *status.LastStartAt
		cloned.LastStartAt = &startedAt
	}
	if status.LastStopAt != nil {
		stoppedAt := *status.LastStopAt
		cloned.LastStopAt = &stoppedAt
	}
	if status.LastFailureAt != nil {
		failureAt := *status.LastFailureAt
		cloned.LastFailureAt = &failureAt
	}
	if status.NextRetryAt != nil {
		retryAt := *status.NextRetryAt
		cloned.NextRetryAt = &retryAt
	}
	return cloned
}

func compareChannelAccountStatuses(a, b ChannelAccountStatus) int {
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

func (m *ChannelManager) MarkRuntimeStarting(runtimeID string, now time.Time) {
	m.updateRuntime(runtimeID, func(status *ChannelAccountStatus) {
		if !status.Enabled || !status.Configured {
			return
		}
		startedAt := now.UTC()
		status.State = ChannelAccountStateRunning
		status.Running = true
		status.LastError = ""
		status.LastStartAt = &startedAt
		status.NextRetryAt = nil
	})
}

func (m *ChannelManager) MarkRuntimeBackingOff(
	runtimeID string,
	now time.Time,
	nextRetryAt time.Time,
	err error,
) {
	m.updateRuntime(runtimeID, func(status *ChannelAccountStatus) {
		if !status.Enabled {
			status.State = ChannelAccountStateDisabled
			status.Running = false
			return
		}
		if !status.Configured {
			status.State = ChannelAccountStateNotConfigured
			status.Running = false
			return
		}
		stoppedAt := now.UTC()
		failureAt := now.UTC()
		retryAt := nextRetryAt.UTC()
		status.State = ChannelAccountStateBackingOff
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

func (m *ChannelManager) MarkRuntimeStopped(runtimeID string, now time.Time, err error) {
	m.updateRuntime(runtimeID, func(status *ChannelAccountStatus) {
		if !status.Enabled {
			status.State = ChannelAccountStateDisabled
			status.Running = false
			return
		}
		if !status.Configured {
			status.State = ChannelAccountStateNotConfigured
			status.Running = false
			return
		}
		stoppedAt := now.UTC()
		status.Running = false
		status.LastStopAt = &stoppedAt
		status.NextRetryAt = nil
		if err != nil {
			failureAt := now.UTC()
			status.State = ChannelAccountStateFailed
			status.LastError = err.Error()
			status.LastFailureAt = &failureAt
			return
		}
		status.State = ChannelAccountStateIdle
		status.LastError = ""
	})
}

func (m *ChannelManager) updateRuntime(runtimeID string, mutate func(*ChannelAccountStatus)) {
	if m == nil || strings.TrimSpace(runtimeID) == "" || mutate == nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, key := range m.runtimeIDs[strings.TrimSpace(runtimeID)] {
		status, ok := m.statuses[key]
		if !ok {
			continue
		}
		mutate(&status)
		m.statuses[key] = status
	}
}

func (m *ChannelManager) HasAccounts() bool {
	if m == nil {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.statuses) > 0
}

func (m *ChannelManager) DebugString() string {
	snapshots := m.Snapshot()
	parts := make([]string, 0, len(snapshots))
	for _, status := range snapshots {
		parts = append(parts, fmt.Sprintf("%s/%s=%s", status.Provider, status.AccountID, status.State))
	}
	return strings.Join(parts, ", ")
}
