package channels

import (
	"fmt"

	"goclaw/internal/agenttools"
	"goclaw/internal/domain"
)

type Registry struct {
	channels  []Channel
	outbounds map[string]Outbound
}

func NewRegistry(entries ...Channel) (*Registry, error) {
	registry := &Registry{
		channels:  make([]Channel, 0, len(entries)),
		outbounds: make(map[string]Outbound),
	}

	for _, entry := range entries {
		if entry == nil {
			continue
		}
		registry.channels = append(registry.channels, entry)
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
