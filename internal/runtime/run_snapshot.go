package runtime

import (
	"context"
	"strings"
	"time"

	"goclaw/internal/domain"
	"goclaw/internal/memory"
	"goclaw/internal/model"
)

// RunSnapshot freezes the control plane for a single reply run so downstream
// runtime paths do not re-read mutable room state mid-flight.
type RunSnapshot struct {
	RunID                    string
	StartedAt                time.Time
	Profile                  domain.Profile
	Person                   domain.Person
	Room                     domain.Room
	Conversation             domain.Conversation
	ConversationSettings     domain.ConversationSettings
	Session                  domain.RoomSession
	Consent                  domain.ConsentPolicy
	TriggerMessageID         domain.MessageID
	TriggerProviderMessageID string
	Retrieval                RetrievalPlan
	MemoryHits               []PromptMemoryHit
	Prompt                   PromptPackage
	ToolPolicy               domain.ToolPermissionPolicy
	ToolingMode              toolingMode
	ReplyPlan                replyModelPlan
}

func (s RunSnapshot) RoomContext() RoomContext {
	return RoomContext{
		Profile:                  s.Profile,
		Person:                   s.Person,
		Room:                     s.Room,
		Conversation:             s.Conversation,
		ConversationSettings:     cloneConversationSettings(s.ConversationSettings),
		Session:                  s.Session,
		Consent:                  cloneConsentPolicy(s.Consent),
		TriggerMessageID:         s.TriggerMessageID,
		TriggerProviderMessageID: strings.TrimSpace(s.TriggerProviderMessageID),
	}
}

func (s RunSnapshot) ModelRequest() model.Request {
	return cloneModelRequest(s.ReplyPlan.Request)
}

func (s *ReplyService) buildRunSnapshot(
	ctx context.Context,
	roomContext RoomContext,
	promptPackage PromptPackage,
) (RunSnapshot, error) {
	plan, err := s.buildReplyModelPlan(ctx, roomContext, promptPackage)
	if err != nil {
		return RunSnapshot{}, err
	}
	return freezeRunSnapshot(
		buildReplyRunID(roomContext.Profile.ID, roomContext.Room.ID, roomContext.TriggerMessageID, s.Now),
		runEventNow(s.Now),
		roomContext,
		promptPackage,
		plan,
	), nil
}

func freezeRunSnapshot(
	runID string,
	startedAt time.Time,
	roomContext RoomContext,
	promptPackage PromptPackage,
	plan replyModelPlan,
) RunSnapshot {
	return RunSnapshot{
		RunID:                    strings.TrimSpace(runID),
		StartedAt:                startedAt.UTC(),
		Profile:                  roomContext.Profile,
		Person:                   roomContext.Person,
		Room:                     roomContext.Room,
		Conversation:             roomContext.Conversation,
		ConversationSettings:     cloneConversationSettings(roomContext.ConversationSettings),
		Session:                  roomContext.Session,
		Consent:                  cloneConsentPolicy(roomContext.Consent),
		TriggerMessageID:         roomContext.TriggerMessageID,
		TriggerProviderMessageID: strings.TrimSpace(roomContext.TriggerProviderMessageID),
		Retrieval:                cloneRetrievalPlan(promptPackage.Retrieval),
		MemoryHits:               clonePromptMemoryHits(promptPackage.MemoryHits),
		Prompt:                   clonePromptPackage(promptPackage),
		ToolPolicy:               cloneToolPermissionPolicy(plan.Policy),
		ToolingMode:              plan.Mode,
		ReplyPlan:                cloneReplyModelPlan(plan),
	}
}

func clonePromptPackage(promptPackage PromptPackage) PromptPackage {
	return PromptPackage{
		SystemBlocks: clonePromptBlocks(promptPackage.SystemBlocks),
		Messages:     clonePromptMessages(promptPackage.Messages),
		MemoryHits:   clonePromptMemoryHits(promptPackage.MemoryHits),
		Retrieval:    cloneRetrievalPlan(promptPackage.Retrieval),
		Skills:       cloneSkillPromptSnapshot(promptPackage.Skills),
	}
}

func clonePromptBlocks(blocks []PromptBlock) []PromptBlock {
	if len(blocks) == 0 {
		return nil
	}
	return append([]PromptBlock(nil), blocks...)
}

func clonePromptMessages(messages []PromptMessage) []PromptMessage {
	if len(messages) == 0 {
		return nil
	}
	return append([]PromptMessage(nil), messages...)
}

func clonePromptMemoryHits(hits []PromptMemoryHit) []PromptMemoryHit {
	if len(hits) == 0 {
		return nil
	}
	cloned := make([]PromptMemoryHit, 0, len(hits))
	for _, hit := range hits {
		cloned = append(cloned, PromptMemoryHit{
			Scope: hit.Scope,
			Result: memory.SearchResult{
				ID:       hit.Result.ID,
				Content:  hit.Result.Content,
				Source:   hit.Result.Source,
				Score:    hit.Result.Score,
				Metadata: cloneStringMap(hit.Result.Metadata),
			},
		})
	}
	return cloned
}

func cloneSkillPromptSnapshot(snapshot skillPromptSnapshot) skillPromptSnapshot {
	return skillPromptSnapshot{
		Root:         snapshot.Root,
		AllSkills:    cloneSkillPromptBlocks(snapshot.AllSkills),
		InlineSkills: cloneSkillPromptBlocks(snapshot.InlineSkills),
	}
}

func cloneSkillPromptBlocks(blocks []skillPromptBlock) []skillPromptBlock {
	if len(blocks) == 0 {
		return nil
	}
	return append([]skillPromptBlock(nil), blocks...)
}

func cloneRetrievalPlan(plan RetrievalPlan) RetrievalPlan {
	return RetrievalPlan{
		Enabled: plan.Enabled,
		Scopes:  append([]memory.Scope(nil), plan.Scopes...),
	}
}

func cloneConsentPolicy(policy domain.ConsentPolicy) domain.ConsentPolicy {
	if policy.ExpiresAt != nil {
		expiresAt := *policy.ExpiresAt
		policy.ExpiresAt = &expiresAt
	}
	return policy
}

func cloneConversationSettings(settings domain.ConversationSettings) domain.ConversationSettings {
	settings.ReadScopes = append([]string(nil), settings.ReadScopes...)
	settings.WriteScopes = append([]string(nil), settings.WriteScopes...)
	return settings
}

func cloneToolPermissionPolicy(policy domain.ToolPermissionPolicy) domain.ToolPermissionPolicy {
	policy.AllowedCommands = append([]string(nil), policy.AllowedCommands...)
	policy.DeniedCommands = append([]string(nil), policy.DeniedCommands...)
	policy.AllowedPaths = append([]string(nil), policy.AllowedPaths...)
	policy.DeniedPaths = append([]string(nil), policy.DeniedPaths...)
	policy.AllowedHosts = append([]string(nil), policy.AllowedHosts...)
	policy.DeniedHosts = append([]string(nil), policy.DeniedHosts...)
	policy.AllowedChannelTools = append([]string(nil), policy.AllowedChannelTools...)
	policy.DeniedChannelTools = append([]string(nil), policy.DeniedChannelTools...)
	policy.AllowedChannelProviders = append([]string(nil), policy.AllowedChannelProviders...)
	policy.DeniedChannelProviders = append([]string(nil), policy.DeniedChannelProviders...)
	policy.AllowedMCPTools = append([]string(nil), policy.AllowedMCPTools...)
	policy.DeniedMCPTools = append([]string(nil), policy.DeniedMCPTools...)
	policy.AllowedMCPServers = append([]string(nil), policy.AllowedMCPServers...)
	policy.DeniedMCPServers = append([]string(nil), policy.DeniedMCPServers...)
	return policy
}

func cloneReplyModelPlan(plan replyModelPlan) replyModelPlan {
	plan.Request = cloneModelRequest(plan.Request)
	return plan
}

func cloneModelRequest(request model.Request) model.Request {
	return model.Request{
		System:   request.System,
		Messages: append([]model.Message(nil), request.Messages...),
	}
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
