package feishuchannel

import (
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"

	channelcore "goclaw/internal/channels"
	"goclaw/internal/domain"
)

func (c *Channel) AccountSnapshots() []channelcore.AccountSnapshot {
	if c == nil {
		return nil
	}

	snapshots := make([]channelcore.AccountSnapshot, 0, len(c.accounts))
	for _, account := range c.accounts {
		runtimeID, listenerAddress, backgroundOnly, _, _ := buildRuntimeMetadata(account)

		snapshots = append(snapshots, channelcore.AccountSnapshot{
			Provider:        domain.ProviderFeishu,
			AccountID:       account.AccountID,
			Name:            account.Settings.Name,
			ProfileID:       account.Settings.ProfileID,
			Enabled:         account.Enabled,
			Configured:      account.Configured,
			ConnectionMode:  account.Settings.ConnectionMode,
			TransportID:     account.TransportID,
			RuntimeID:       runtimeID,
			WebhookAddress:  listenerAddress,
			WebhookPath:     account.Settings.WebhookPath,
			BackgroundOnly:  backgroundOnly,
			SupportsIngress: account.Enabled,
		})
	}
	return snapshots
}

func (c *Channel) TransportSnapshots() []channelcore.TransportSnapshot {
	if c == nil {
		return nil
	}

	longpollManaged := c.longpollLifecycleAvailable()
	snapshotIndex := make(map[string]int)
	snapshots := make([]channelcore.TransportSnapshot, 0)
	for _, account := range c.accounts {
		if !account.Enabled {
			continue
		}

		runtimeID, listenerAddress, _, kind, lifecycleMode := buildRuntimeMetadata(account)
		index, ok := snapshotIndex[runtimeID]
		if !ok {
			snapshots = append(snapshots, channelcore.TransportSnapshot{
				Provider:        domain.ProviderFeishu,
				Name:            runtimeID,
				RuntimeID:       runtimeID,
				Kind:            kind,
				LifecycleMode:   lifecycleMode,
				OperatorManaged: kind == channelcore.TransportRuntimeKindLongpoll && longpollManaged,
				Shared:          lifecycleMode == channelcore.TransportLifecycleModeSharedRuntime,
				Address:         listenerAddress,
			})
			index = len(snapshots) - 1
			snapshotIndex[runtimeID] = index
		}

		snapshots[index].Bindings = append(snapshots[index].Bindings, channelcore.TransportBinding{
			AccountID:      account.AccountID,
			ProfileID:      account.Settings.ProfileID,
			TransportID:    account.TransportID,
			ConnectionMode: account.Settings.ConnectionMode,
			WebhookPath:    strings.TrimSpace(account.Settings.WebhookPath),
			Configured:     account.Configured,
		})
	}

	for i := range snapshots {
		slices.SortFunc(snapshots[i].Bindings, func(a, b channelcore.TransportBinding) int {
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
		})
	}
	slices.SortFunc(snapshots, func(a, b channelcore.TransportSnapshot) int {
		switch {
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
	})

	return snapshots
}

func (c *Channel) AccountLifecycleSpecs() []channelcore.AccountLifecycleSpec {
	if c == nil {
		return nil
	}

	longpollManaged := c.longpollLifecycleAvailable()
	snapshots := make([]channelcore.AccountLifecycleSpec, 0, len(c.accounts))
	for _, account := range c.accounts {
		runtimeID, _, _, _, lifecycleMode := buildRuntimeMetadata(account)
		operatorManaged := account.Enabled &&
			account.Configured &&
			isLongpollMode(account.Settings.ConnectionMode) &&
			longpollManaged
		snapshots = append(snapshots, channelcore.AccountLifecycleSpec{
			Provider:        domain.ProviderFeishu,
			AccountID:       account.AccountID,
			ProfileID:       account.Settings.ProfileID,
			Enabled:         account.Enabled,
			Configured:      account.Configured,
			RuntimeID:       runtimeID,
			ConnectionMode:  account.Settings.ConnectionMode,
			LifecycleMode:   lifecycleMode,
			OperatorManaged: operatorManaged,
			SupportsStart:   operatorManaged,
			SupportsStop:    operatorManaged,
			SupportsRestart: operatorManaged,
			Reason:          lifecycleReason(account, longpollManaged),
		})
	}

	slices.SortFunc(snapshots, func(a, b channelcore.AccountLifecycleSpec) int {
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
		default:
			return 0
		}
	})

	return snapshots
}

func buildRuntimeMetadata(
	account resolvedAccount,
) (
	runtimeID string,
	listenerAddress string,
	backgroundOnly bool,
	kind channelcore.TransportRuntimeKind,
	lifecycleMode channelcore.TransportLifecycleMode,
) {
	if isWebhookMode(account.Settings.ConnectionMode) {
		listenerAddress = net.JoinHostPort(account.Settings.WebhookHost, strconv.Itoa(account.Settings.WebhookPort))
		return fmt.Sprintf("webhook[%s]", listenerAddress),
			listenerAddress,
			false,
			channelcore.TransportRuntimeKindWebhook,
			channelcore.TransportLifecycleModeSharedRuntime
	}

	return account.TransportID,
		"",
		true,
		channelcore.TransportRuntimeKindLongpoll,
		channelcore.TransportLifecycleModeDedicatedRuntime
}

func lifecycleReason(account resolvedAccount, longpollManaged bool) string {
	switch {
	case !account.Enabled:
		return "account is disabled in config; no operator lifecycle hook is exposed"
	case isWebhookMode(account.Settings.ConnectionMode):
		return "webhook accounts share a process-level listener; per-account lifecycle control is not exposed yet"
	case !account.Configured:
		return "longpoll account is not fully configured; lifecycle control remains unavailable"
	case longpollManaged:
		return "longpoll runtime is managed by the channel lifecycle controller"
	default:
		return "longpoll accounts use a dedicated runtime shape, but no gateway runtime host is attached yet"
	}
}
