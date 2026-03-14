package gateway

import (
	"fmt"
	"time"
)

type StatusSnapshot struct {
	Ready      bool                   `json:"ready"`
	Issues     []string               `json:"issues"`
	Channels   []ChannelAccountStatus `json:"channels"`
	Transports []TransportStatus      `json:"transports"`
}

func (s *Server) StatusSnapshot() StatusSnapshot {
	if s == nil {
		return StatusSnapshot{}
	}
	var channels []ChannelAccountStatus
	if s.channelManager != nil {
		channels = s.channelManager.Snapshot()
	}
	var transports []TransportStatus
	if s.transportInventory != nil {
		transports = s.transportInventory.Snapshot()
	}
	issues := collectGatewayIssues(channels, transports)
	return StatusSnapshot{
		Ready:      len(issues) == 0,
		Issues:     issues,
		Channels:   channels,
		Transports: transports,
	}
}

func computeGatewayReady(channels []ChannelAccountStatus, transports []TransportStatus) bool {
	return len(collectGatewayIssues(channels, transports)) == 0
}

func collectGatewayIssues(channels []ChannelAccountStatus, transports []TransportStatus) []string {
	issues := make([]string, 0)
	issues = append(issues, collectChannelIssues(channels)...)
	issues = append(issues, collectTransportIssues(transports)...)
	if len(issues) == 0 {
		return nil
	}
	return issues
}

func collectChannelIssues(channels []ChannelAccountStatus) []string {
	if len(channels) == 0 {
		return nil
	}
	issues := make([]string, 0)
	for _, channel := range channels {
		switch {
		case channel.Enabled && !channel.Configured:
			issues = append(issues, fmt.Sprintf("%s/%s: enabled but not configured", channel.Provider, channel.AccountID))
		case channel.State == ChannelAccountStateBackingOff:
			if channel.NextRetryAt != nil && channel.LastError != "" {
				issues = append(
					issues,
					fmt.Sprintf(
						"%s/%s: runtime backing off until %s: %s",
						channel.Provider,
						channel.AccountID,
						channel.NextRetryAt.UTC().Format(time.RFC3339),
						channel.LastError,
					),
				)
				continue
			}
			issues = append(issues, fmt.Sprintf("%s/%s: runtime backing off", channel.Provider, channel.AccountID))
		case channel.State == ChannelAccountStateFailed:
			if channel.LastError != "" {
				issues = append(
					issues,
					fmt.Sprintf("%s/%s: runtime failed: %s", channel.Provider, channel.AccountID, channel.LastError),
				)
				continue
			}
			issues = append(issues, fmt.Sprintf("%s/%s: runtime failed", channel.Provider, channel.AccountID))
		}
	}
	return issues
}

func collectTransportIssues(transports []TransportStatus) []string {
	if len(transports) == 0 {
		return nil
	}
	issues := make([]string, 0)
	for _, transport := range transports {
		if transport.Source != TransportSourceWorker {
			continue
		}
		switch transport.State {
		case TransportStateBackingOff:
			if transport.NextRetryAt != nil && transport.LastError != "" {
				issues = append(
					issues,
					fmt.Sprintf(
						"%s: transport backing off until %s: %s",
						transport.Name,
						transport.NextRetryAt.UTC().Format(time.RFC3339),
						transport.LastError,
					),
				)
				continue
			}
			issues = append(issues, fmt.Sprintf("%s: transport backing off", transport.Name))
		case TransportStateFailed:
			if transport.LastError != "" {
				issues = append(issues, fmt.Sprintf("%s: transport failed: %s", transport.Name, transport.LastError))
				continue
			}
			issues = append(issues, fmt.Sprintf("%s: transport failed", transport.Name))
		}
	}
	if len(issues) == 0 {
		return nil
	}
	return issues
}
