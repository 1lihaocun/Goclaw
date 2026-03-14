package channels

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"goclaw/internal/agenttools"
	"goclaw/internal/domain"
)

var (
	ErrAccountLifecycleUnsupported   = errors.New("channels: account lifecycle control unsupported")
	ErrAccountLifecycleTargetInvalid = errors.New("channels: invalid account lifecycle target")
)

type Registry struct {
	channels             []Channel
	outbounds            map[string]Outbound
	lifecycleControllers map[domain.Provider]AccountLifecycleController
}

func NewRegistry(entries ...Channel) (*Registry, error) {
	registry := &Registry{
		channels:             make([]Channel, 0, len(entries)),
		outbounds:            make(map[string]Outbound),
		lifecycleControllers: make(map[domain.Provider]AccountLifecycleController),
	}

	for _, entry := range entries {
		if entry == nil {
			continue
		}
		registry.channels = append(registry.channels, entry)
		if controller, ok := entry.(AccountLifecycleController); ok {
			provider := entry.Provider()
			if _, exists := registry.lifecycleControllers[provider]; exists {
				return nil, fmt.Errorf("channels: duplicate lifecycle controller for provider %s", provider)
			}
			registry.lifecycleControllers[provider] = controller
		}
		for _, outbound := range entry.Outbounds() {
			if outbound == nil {
				continue
			}
			profileID := outbound.ProfileID()
			if profileID == "" {
				return nil, fmt.Errorf("channels: missing outbound profile id for provider %s", outbound.Provider())
			}
			outboundKey := buildOutboundKey(outbound.Provider(), profileID)
			if existing, ok := registry.outbounds[outboundKey]; ok {
				return nil, fmt.Errorf(
					"channels: duplicate outbound key %q for providers %s and %s",
					outboundKey,
					existing.Provider(),
					outbound.Provider(),
				)
			}
			registry.outbounds[outboundKey] = outbound
		}
	}

	return registry, nil
}

func (r *Registry) Outbound(provider domain.Provider, profileID domain.ProfileID) (Outbound, bool) {
	if r == nil {
		return nil, false
	}
	outbound, ok := r.outbounds[buildOutboundKey(provider, profileID)]
	return outbound, ok
}

func buildOutboundKey(provider domain.Provider, profileID domain.ProfileID) string {
	return string(provider) + ":" + string(profileID)
}

func (r *Registry) HTTPRoutes(params BuildRuntimeParams) ([]HTTPRoute, error) {
	if r == nil {
		return nil, nil
	}

	routes := make([]HTTPRoute, 0)
	for _, channel := range r.channels {
		channelRoutes, err := channel.HTTPRoutes(params)
		if err != nil {
			return nil, err
		}
		routes = append(routes, channelRoutes...)
	}
	return routes, nil
}

func (r *Registry) Transports(params BuildRuntimeParams) ([]Transport, error) {
	if r == nil {
		return nil, nil
	}

	transports := make([]Transport, 0)
	for _, channel := range r.channels {
		channelTransports, err := channel.Transports(params)
		if err != nil {
			return nil, err
		}
		transports = append(transports, channelTransports...)
	}
	return transports, nil
}

func (r *Registry) AgentToolProviders(params ToolBuildParams) ([]agenttools.Provider, error) {
	if r == nil {
		return nil, nil
	}

	providers := make([]agenttools.Provider, 0)
	for _, channel := range r.channels {
		channelProviders, err := channel.AgentToolProviders(params)
		if err != nil {
			return nil, err
		}
		providers = append(providers, channelProviders...)
	}
	return providers, nil
}

func (r *Registry) BindRuntime(params BuildRuntimeParams) {
	if r == nil {
		return
	}
	for _, channel := range r.channels {
		binder, ok := channel.(RuntimeBinder)
		if !ok {
			continue
		}
		binder.BindRuntime(params)
	}
}

func (r *Registry) AccountSnapshots() []AccountSnapshot {
	if r == nil {
		return nil
	}

	snapshots := make([]AccountSnapshot, 0)
	for _, channel := range r.channels {
		reporter, ok := channel.(AccountSnapshotReporter)
		if !ok {
			continue
		}
		snapshots = append(snapshots, reporter.AccountSnapshots()...)
	}

	slices.SortFunc(snapshots, func(a, b AccountSnapshot) int {
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
	})

	return snapshots
}

func (r *Registry) TransportSnapshots() []TransportSnapshot {
	if r == nil {
		return nil
	}

	snapshots := make([]TransportSnapshot, 0)
	for _, channel := range r.channels {
		reporter, ok := channel.(TransportSnapshotReporter)
		if !ok {
			continue
		}
		channelSnapshots := reporter.TransportSnapshots()
		for i := range channelSnapshots {
			channelSnapshots[i].Bindings = append([]TransportBinding(nil), channelSnapshots[i].Bindings...)
			slices.SortFunc(channelSnapshots[i].Bindings, compareTransportBindings)
		}
		snapshots = append(snapshots, channelSnapshots...)
	}

	slices.SortFunc(snapshots, compareTransportSnapshots)
	return snapshots
}

func compareTransportBindings(a, b TransportBinding) int {
	switch {
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
	case a.TransportID != b.TransportID:
		if a.TransportID < b.TransportID {
			return -1
		}
		return 1
	default:
		return 0
	}
}

func compareTransportSnapshots(a, b TransportSnapshot) int {
	switch {
	case a.Provider != b.Provider:
		if a.Provider < b.Provider {
			return -1
		}
		return 1
	case a.RuntimeID != b.RuntimeID:
		if a.RuntimeID < b.RuntimeID {
			return -1
		}
		return 1
	case a.Name != b.Name:
		if a.Name < b.Name {
			return -1
		}
		return 1
	default:
		return 0
	}
}

func (r *Registry) AccountLifecycleSpecs() []AccountLifecycleSpec {
	if r == nil {
		return nil
	}

	snapshots := make([]AccountLifecycleSpec, 0)
	for _, channel := range r.channels {
		reporter, ok := channel.(AccountLifecycleSpecReporter)
		if !ok {
			continue
		}
		snapshots = append(snapshots, reporter.AccountLifecycleSpecs()...)
	}

	slices.SortFunc(snapshots, compareAccountLifecycleSpecs)
	return snapshots
}

func compareAccountLifecycleSpecs(a, b AccountLifecycleSpec) int {
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

func (r *Registry) StartAccount(ctx context.Context, account AccountRef) error {
	return r.dispatchAccountLifecycle(account, func(
		controller AccountLifecycleController,
		account AccountRef,
	) error {
		return controller.StartAccount(ctx, account)
	})
}

func (r *Registry) StopAccount(ctx context.Context, account AccountRef) error {
	return r.dispatchAccountLifecycle(account, func(
		controller AccountLifecycleController,
		account AccountRef,
	) error {
		return controller.StopAccount(ctx, account)
	})
}

func (r *Registry) RestartAccount(ctx context.Context, account AccountRef) error {
	return r.dispatchAccountLifecycle(account, func(
		controller AccountLifecycleController,
		account AccountRef,
	) error {
		return controller.RestartAccount(ctx, account)
	})
}

func (r *Registry) dispatchAccountLifecycle(
	account AccountRef,
	run func(AccountLifecycleController, AccountRef) error,
) error {
	if r == nil {
		return fmt.Errorf("%w: registry is nil", ErrAccountLifecycleUnsupported)
	}

	normalizedAccount := normalizeAccountRef(account)
	if normalizedAccount.Provider == "" {
		return fmt.Errorf("%w: missing provider", ErrAccountLifecycleTargetInvalid)
	}
	if normalizedAccount.AccountID == "" && normalizedAccount.ProfileID == "" {
		return fmt.Errorf("%w: missing account_id or profile_id", ErrAccountLifecycleTargetInvalid)
	}

	controller, ok := r.lifecycleControllers[normalizedAccount.Provider]
	if !ok {
		return fmt.Errorf("%w: provider=%s", ErrAccountLifecycleUnsupported, normalizedAccount.Provider)
	}

	return run(controller, normalizedAccount)
}

func normalizeAccountRef(account AccountRef) AccountRef {
	return AccountRef{
		Provider:  account.Provider,
		AccountID: strings.TrimSpace(account.AccountID),
		ProfileID: domain.ProfileID(strings.TrimSpace(string(account.ProfileID))),
	}
}
