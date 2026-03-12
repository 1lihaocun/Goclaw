package agenttools

import (
	"strings"

	"goclaw/internal/domain"
)

func AllowsChannelTool(
	policy domain.ToolPermissionPolicy,
	session SessionContext,
	definition Definition,
) bool {
	if definition.Source != SourceChannel {
		return false
	}
	if strings.TrimSpace(string(definition.Provider)) == "" {
		return false
	}
	if session.Provider != definition.Provider {
		return false
	}

	if listContains(policy.DeniedChannelTools, definition.Name) {
		return false
	}
	if listContains(policy.DeniedChannelProviders, string(definition.Provider)) {
		return false
	}

	mode := channelModeForSafety(policy, definition.SafetyClass)
	switch mode {
	case domain.ToolPermissionAllowAll:
		return true
	case domain.ToolPermissionAllowList:
		return listContains(policy.AllowedChannelTools, definition.Name) ||
			listContains(policy.AllowedChannelProviders, string(definition.Provider))
	default:
		return false
	}
}

func channelModeForSafety(
	policy domain.ToolPermissionPolicy,
	safety SafetyClass,
) domain.ToolPermissionMode {
	switch safety {
	case SafetyIntrospection:
		return normalizeMode(policy.ChannelIntrospectionMode)
	case SafetyReadOnly:
		return normalizeMode(policy.ChannelReadMode)
	case SafetyMutating:
		return normalizeMode(policy.ChannelWriteMode)
	case SafetySensitive:
		return normalizeMode(policy.ChannelSensitiveMode)
	default:
		return domain.ToolPermissionDenyAll
	}
}

func normalizeMode(mode domain.ToolPermissionMode) domain.ToolPermissionMode {
	switch mode {
	case domain.ToolPermissionAllowAll, domain.ToolPermissionAllowList:
		return mode
	default:
		return domain.ToolPermissionDenyAll
	}
}

func listContains(values []string, target string) bool {
	target = strings.TrimSpace(strings.ToLower(target))
	if target == "" {
		return false
	}
	for _, value := range values {
		if strings.TrimSpace(strings.ToLower(value)) == target {
			return true
		}
	}
	return false
}
