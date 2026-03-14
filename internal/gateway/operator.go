package gateway

import (
	"context"
	"errors"
	"fmt"
	"strings"

	channelcore "goclaw/internal/channels"
	"goclaw/internal/domain"
)

type AccountSelector struct {
	Provider  domain.Provider  `json:"provider,omitempty"`
	AccountID string           `json:"account_id,omitempty"`
	ProfileID domain.ProfileID `json:"profile_id,omitempty"`
}

type AccountInspectView struct {
	Target          channelcore.AccountRef           `json:"target"`
	Account         channelcore.AccountSnapshot      `json:"account"`
	Runtime         ChannelAccountStatus             `json:"runtime"`
	Lifecycle       channelcore.AccountLifecycleSpec `json:"lifecycle"`
	Transport       *channelcore.TransportSnapshot   `json:"transport,omitempty"`
	TransportStatus *TransportStatus                 `json:"transport_status,omitempty"`
	Issues          []string                         `json:"issues,omitempty"`
}

type LifecycleAction string

const (
	LifecycleActionStart   LifecycleAction = "start"
	LifecycleActionStop    LifecycleAction = "stop"
	LifecycleActionRestart LifecycleAction = "restart"
)

type LifecycleActionResult struct {
	Action          LifecycleAction                    `json:"action"`
	Target          channelcore.AccountRef             `json:"target"`
	RuntimeID       string                             `json:"runtime_id,omitempty"`
	ConnectionMode  string                             `json:"connection_mode,omitempty"`
	LifecycleMode   channelcore.TransportLifecycleMode `json:"lifecycle_mode,omitempty"`
	OperatorManaged bool                               `json:"operator_managed"`
	Supported       bool                               `json:"supported"`
	Dispatched      bool                               `json:"dispatched"`
	Message         string                             `json:"message,omitempty"`
}

type resolvedGatewayAccount struct {
	target          channelcore.AccountRef
	account         channelcore.AccountSnapshot
	runtime         ChannelAccountStatus
	lifecycle       channelcore.AccountLifecycleSpec
	transport       *channelcore.TransportSnapshot
	transportStatus *TransportStatus
}

func (s *Server) InspectAccount(target AccountSelector) (AccountInspectView, error) {
	resolved, err := s.resolveAccount(target)
	if err != nil {
		return AccountInspectView{}, err
	}

	return AccountInspectView{
		Target:          resolved.target,
		Account:         resolved.account,
		Runtime:         resolved.runtime,
		Lifecycle:       resolved.lifecycle,
		Transport:       cloneTransportSnapshotPointer(resolved.transport),
		TransportStatus: cloneTransportStatusPointer(resolved.transportStatus),
		Issues:          collectChannelIssues([]ChannelAccountStatus{resolved.runtime}),
	}, nil
}

func (s *Server) StartAccount(ctx context.Context, target AccountSelector) (LifecycleActionResult, error) {
	return s.runLifecycleAction(ctx, target, LifecycleActionStart)
}

func (s *Server) StopAccount(ctx context.Context, target AccountSelector) (LifecycleActionResult, error) {
	return s.runLifecycleAction(ctx, target, LifecycleActionStop)
}

func (s *Server) RestartAccount(ctx context.Context, target AccountSelector) (LifecycleActionResult, error) {
	return s.runLifecycleAction(ctx, target, LifecycleActionRestart)
}

func (s *Server) runLifecycleAction(
	ctx context.Context,
	target AccountSelector,
	action LifecycleAction,
) (LifecycleActionResult, error) {
	resolved, err := s.resolveAccount(target)
	if err != nil {
		return LifecycleActionResult{}, err
	}

	result := LifecycleActionResult{
		Action:          action,
		Target:          resolved.target,
		RuntimeID:       resolved.runtime.RuntimeID,
		ConnectionMode:  resolved.account.ConnectionMode,
		LifecycleMode:   resolved.lifecycle.LifecycleMode,
		OperatorManaged: resolved.lifecycle.OperatorManaged,
		Supported:       lifecycleActionSupported(resolved.lifecycle, action),
		Dispatched:      false,
	}
	if !result.Supported {
		result.Message = unsupportedLifecycleMessage(resolved.lifecycle, action)
		return result, nil
	}

	if s == nil || s.App == nil || s.App.Channels == nil {
		return LifecycleActionResult{}, errors.New("gateway: missing runtime app")
	}

	var dispatchErr error
	switch action {
	case LifecycleActionStart:
		dispatchErr = s.App.Channels.StartAccount(ctx, resolved.target)
	case LifecycleActionStop:
		dispatchErr = s.App.Channels.StopAccount(ctx, resolved.target)
	case LifecycleActionRestart:
		dispatchErr = s.App.Channels.RestartAccount(ctx, resolved.target)
	default:
		return LifecycleActionResult{}, fmt.Errorf("gateway: unknown lifecycle action %q", action)
	}
	if dispatchErr != nil {
		if errors.Is(dispatchErr, channelcore.ErrAccountLifecycleUnsupported) {
			result.Message = strings.TrimSpace(dispatchErr.Error())
			return result, nil
		}
		return LifecycleActionResult{}, dispatchErr
	}

	result.Dispatched = true
	result.Message = fmt.Sprintf("%s dispatched to channel lifecycle controller", action)
	return result, nil
}

func lifecycleActionSupported(
	spec channelcore.AccountLifecycleSpec,
	action LifecycleAction,
) bool {
	switch action {
	case LifecycleActionStart:
		return spec.SupportsStart
	case LifecycleActionStop:
		return spec.SupportsStop
	case LifecycleActionRestart:
		return spec.SupportsRestart
	default:
		return false
	}
}

func unsupportedLifecycleMessage(
	spec channelcore.AccountLifecycleSpec,
	action LifecycleAction,
) string {
	if strings.TrimSpace(spec.Reason) != "" {
		return spec.Reason
	}
	return fmt.Sprintf("%s is not supported for this account", action)
}

func (s *Server) resolveAccount(target AccountSelector) (resolvedGatewayAccount, error) {
	if s == nil || s.App == nil || s.App.Channels == nil {
		return resolvedGatewayAccount{}, errors.New("gateway: missing runtime app")
	}

	normalized := normalizeAccountSelector(target)
	accountSnapshots := s.App.Channels.AccountSnapshots()
	if len(accountSnapshots) == 0 {
		return resolvedGatewayAccount{}, errors.New("gateway: no channel account snapshots available")
	}

	matches := make([]channelcore.AccountSnapshot, 0, 1)
	for _, snapshot := range accountSnapshots {
		if matchesAccountSelector(snapshot, normalized) {
			matches = append(matches, snapshot)
		}
	}
	if len(matches) == 0 {
		return resolvedGatewayAccount{}, fmt.Errorf(
			"gateway: no channel account matched provider=%q account_id=%q profile_id=%q",
			normalized.Provider,
			normalized.AccountID,
			normalized.ProfileID,
		)
	}
	if len(matches) > 1 {
		candidates := make([]string, 0, len(matches))
		for _, match := range matches {
			candidates = append(candidates, fmt.Sprintf("%s/%s(%s)", match.Provider, match.AccountID, match.ProfileID))
		}
		return resolvedGatewayAccount{}, fmt.Errorf(
			"gateway: ambiguous account target provider=%q account_id=%q profile_id=%q candidates=%s",
			normalized.Provider,
			normalized.AccountID,
			normalized.ProfileID,
			strings.Join(candidates, ", "),
		)
	}

	account := matches[0]
	runtime := s.findRuntimeStatus(account)
	lifecycle := s.findLifecycleSpec(account, runtime)
	transport := s.findTransportSnapshot(runtime.RuntimeID)
	transportStatus := s.findTransportStatus(runtime.RuntimeID)

	return resolvedGatewayAccount{
		target: channelcore.AccountRef{
			Provider:  account.Provider,
			AccountID: strings.TrimSpace(account.AccountID),
			ProfileID: account.ProfileID,
		},
		account:         account,
		runtime:         runtime,
		lifecycle:       lifecycle,
		transport:       transport,
		transportStatus: transportStatus,
	}, nil
}

func normalizeAccountSelector(target AccountSelector) AccountSelector {
	return AccountSelector{
		Provider:  target.Provider,
		AccountID: strings.TrimSpace(target.AccountID),
		ProfileID: domain.ProfileID(strings.TrimSpace(string(target.ProfileID))),
	}
}

func matchesAccountSelector(snapshot channelcore.AccountSnapshot, selector AccountSelector) bool {
	if selector.Provider != "" && snapshot.Provider != selector.Provider {
		return false
	}
	if selector.AccountID != "" && strings.TrimSpace(snapshot.AccountID) != selector.AccountID {
		return false
	}
	if selector.ProfileID != "" && snapshot.ProfileID != selector.ProfileID {
		return false
	}
	return true
}

func (s *Server) findRuntimeStatus(account channelcore.AccountSnapshot) ChannelAccountStatus {
	if s == nil || s.channelManager == nil {
		return ChannelAccountStatus{
			Provider:        account.Provider,
			AccountID:       account.AccountID,
			Name:            account.Name,
			ProfileID:       account.ProfileID,
			Enabled:         account.Enabled,
			Configured:      account.Configured,
			ConnectionMode:  account.ConnectionMode,
			TransportID:     account.TransportID,
			RuntimeID:       account.RuntimeID,
			WebhookAddress:  account.WebhookAddress,
			WebhookPath:     account.WebhookPath,
			BackgroundOnly:  account.BackgroundOnly,
			SupportsIngress: account.SupportsIngress,
			State:           resolveInitialChannelAccountState(account),
		}
	}

	for _, status := range s.channelManager.Snapshot() {
		if status.Provider == account.Provider &&
			status.AccountID == account.AccountID &&
			status.ProfileID == account.ProfileID {
			return status
		}
	}

	return ChannelAccountStatus{
		Provider:        account.Provider,
		AccountID:       account.AccountID,
		Name:            account.Name,
		ProfileID:       account.ProfileID,
		Enabled:         account.Enabled,
		Configured:      account.Configured,
		ConnectionMode:  account.ConnectionMode,
		TransportID:     account.TransportID,
		RuntimeID:       account.RuntimeID,
		WebhookAddress:  account.WebhookAddress,
		WebhookPath:     account.WebhookPath,
		BackgroundOnly:  account.BackgroundOnly,
		SupportsIngress: account.SupportsIngress,
		State:           resolveInitialChannelAccountState(account),
	}
}

func (s *Server) findLifecycleSpec(
	account channelcore.AccountSnapshot,
	runtime ChannelAccountStatus,
) channelcore.AccountLifecycleSpec {
	if s != nil && s.App != nil && s.App.Channels != nil {
		for _, spec := range s.App.Channels.AccountLifecycleSpecs() {
			if spec.Provider == account.Provider &&
				spec.AccountID == account.AccountID &&
				spec.ProfileID == account.ProfileID {
				return spec
			}
		}
	}

	return channelcore.AccountLifecycleSpec{
		Provider:        account.Provider,
		AccountID:       account.AccountID,
		ProfileID:       account.ProfileID,
		Enabled:         account.Enabled,
		Configured:      account.Configured,
		RuntimeID:       runtime.RuntimeID,
		ConnectionMode:  account.ConnectionMode,
		LifecycleMode:   channelcore.TransportLifecycleModeDedicatedRuntime,
		OperatorManaged: false,
		SupportsStart:   false,
		SupportsStop:    false,
		SupportsRestart: false,
		Reason:          "channel does not expose lifecycle metadata",
	}
}

func (s *Server) findTransportSnapshot(runtimeID string) *channelcore.TransportSnapshot {
	if s == nil || s.App == nil || s.App.Channels == nil || strings.TrimSpace(runtimeID) == "" {
		return nil
	}
	for _, snapshot := range s.App.Channels.TransportSnapshots() {
		if snapshot.RuntimeID == strings.TrimSpace(runtimeID) {
			cloned := snapshot
			cloned.Bindings = append([]channelcore.TransportBinding(nil), snapshot.Bindings...)
			return &cloned
		}
	}
	return nil
}

func (s *Server) findTransportStatus(runtimeID string) *TransportStatus {
	if s == nil || s.transportInventory == nil || strings.TrimSpace(runtimeID) == "" {
		return nil
	}
	for _, status := range s.transportInventory.Snapshot() {
		if status.Name == strings.TrimSpace(runtimeID) {
			cloned := cloneTransportStatus(status)
			return &cloned
		}
	}
	return nil
}

func cloneTransportSnapshotPointer(snapshot *channelcore.TransportSnapshot) *channelcore.TransportSnapshot {
	if snapshot == nil {
		return nil
	}
	cloned := *snapshot
	cloned.Bindings = append([]channelcore.TransportBinding(nil), snapshot.Bindings...)
	return &cloned
}

func cloneTransportStatusPointer(status *TransportStatus) *TransportStatus {
	if status == nil {
		return nil
	}
	cloned := cloneTransportStatus(*status)
	return &cloned
}
