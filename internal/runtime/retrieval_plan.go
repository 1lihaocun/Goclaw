package runtime

import (
	"goclaw/internal/domain"
	"goclaw/internal/memory"
)

type RetrievalPlan struct {
	Enabled bool
	Scopes  []memory.Scope
}

func BuildRetrievalPlan(
	profileID domain.ProfileID,
	roomID domain.RoomID,
	conversationID domain.ConversationID,
	personID domain.PersonID,
	consent domain.ConsentAccessLevel,
	settings domain.ConversationSettings,
	memoryEnabled bool,
) RetrievalPlan {
	settings = normalizeConversationSettings(settings, conversationID)
	if !memoryEnabled || !settings.Enabled {
		return RetrievalPlan{Enabled: false}
	}

	scopes := make([]memory.Scope, 0, 4)
	if conversationID != "" && conversationReadAllows(settings, memory.ScopeConversation) {
		scopes = append(scopes, memory.Scope{
			Kind:           memory.ScopeConversation,
			ProfileID:      profileID,
			RoomID:         roomID,
			ConversationID: conversationID,
			PersonID:       personID,
		})
	}
	if conversationReadAllows(settings, memory.ScopeRoom) {
		scopes = append(scopes, memory.Scope{
			Kind:           memory.ScopeRoom,
			ProfileID:      profileID,
			RoomID:         roomID,
			ConversationID: conversationID,
			PersonID:       personID,
		})
	}

	switch consent {
	case domain.ConsentSummary:
		if conversationReadAllows(settings, memory.ScopePersonSummary) {
			scopes = append(scopes, memory.Scope{
				Kind:           memory.ScopePersonSummary,
				ProfileID:      profileID,
				RoomID:         roomID,
				ConversationID: conversationID,
				PersonID:       personID,
			})
		}
	case domain.ConsentFull:
		if conversationReadAllows(settings, memory.ScopePersonSummary) {
			scopes = append(scopes, memory.Scope{
				Kind:           memory.ScopePersonSummary,
				ProfileID:      profileID,
				RoomID:         roomID,
				ConversationID: conversationID,
				PersonID:       personID,
			})
		}
		if conversationReadAllows(settings, memory.ScopePersonPrivate) {
			scopes = append(scopes, memory.Scope{
				Kind:           memory.ScopePersonPrivate,
				ProfileID:      profileID,
				RoomID:         roomID,
				ConversationID: conversationID,
				PersonID:       personID,
			})
		}
	}
	if len(scopes) == 0 {
		return RetrievalPlan{Enabled: false}
	}

	return RetrievalPlan{
		Enabled: true,
		Scopes:  scopes,
	}
}
