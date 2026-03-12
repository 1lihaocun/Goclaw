package runtime

import (
	"strings"

	"goclaw/internal/domain"
	"goclaw/internal/memory"
)

func normalizeConversationSettings(
	settings domain.ConversationSettings,
	conversationID domain.ConversationID,
) domain.ConversationSettings {
	if settings.ConversationID == "" {
		settings.ConversationID = conversationID
	}
	if !settings.Explicit {
		defaults := domain.DefaultConversationSettings(settings.ConversationID)
		if !settings.Enabled {
			settings.Enabled = defaults.Enabled
		}
		if !settings.AllowProviderMemory {
			settings.AllowProviderMemory = defaults.AllowProviderMemory
		}
		if !settings.AllowMarkdownMemory {
			settings.AllowMarkdownMemory = defaults.AllowMarkdownMemory
		}
		if !settings.WriteAssistantToPersonPrivate {
			settings.WriteAssistantToPersonPrivate = defaults.WriteAssistantToPersonPrivate
		}
		if len(settings.ReadScopes) == 0 {
			settings.ReadScopes = defaults.ReadScopes
		}
		if len(settings.WriteScopes) == 0 {
			settings.WriteScopes = defaults.WriteScopes
		}
		if settings.JournalMode == "" {
			settings.JournalMode = defaults.JournalMode
		}
	}
	return settings
}

func conversationReadAllows(
	settings domain.ConversationSettings,
	scope memory.ScopeKind,
) bool {
	return conversationScopeAllowed(settings.ReadScopes, scope)
}

func conversationWriteAllows(
	settings domain.ConversationSettings,
	scope memory.ScopeKind,
) bool {
	return conversationScopeAllowed(settings.WriteScopes, scope)
}

func conversationScopeAllowed(scopes []string, scope memory.ScopeKind) bool {
	for _, item := range scopes {
		if strings.TrimSpace(item) == string(scope) {
			return true
		}
	}
	return false
}
