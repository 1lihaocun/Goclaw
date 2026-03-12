package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"goclaw/internal/agenttools"
	"goclaw/internal/domain"
	sqlitestore "goclaw/internal/store/sqlite"
	toolruntime "goclaw/internal/tools"
)

func (s *ReplyService) toolingAuditHooks(
	roomContext RoomContext,
	policy domain.ToolPermissionPolicy,
	mode toolingMode,
) (
	toolruntime.RunStartHook,
	toolruntime.RunFinishHook,
	toolruntime.InvocationStartHook,
	toolruntime.InvocationFinishHook,
) {
	if s.Repos.ReplyRuns == (sqlitestore.ReplyRunsRepository{}) ||
		s.Repos.ToolInvocations == (sqlitestore.ToolInvocationsRepository{}) {
		return nil, nil, nil, nil
	}

	now := s.Now
	if now == nil {
		now = time.Now
	}
	registry := s.toolRegistry()
	runID := buildReplyRunID(roomContext.Profile.ID, roomContext.Room.ID, roomContext.TriggerMessageID, now)

	onRunStart := func(ctx context.Context) error {
		return s.Repos.ReplyRuns.Insert(ctx, domain.ReplyRun{
			ID:               runID,
			ProfileID:        roomContext.Profile.ID,
			RoomID:           roomContext.Room.ID,
			ConversationID:   roomContext.Conversation.ID,
			SessionID:        roomContext.Session.ID,
			InboundMessageID: roomContext.TriggerMessageID,
			ModelProvider:    s.Model.Name(),
			ToolingMode:      string(mode),
			Status:           domain.ReplyRunRunning,
			StartedAt:        now().UTC(),
		})
	}
	onRunFinish := func(ctx context.Context, _ string, runErr error) error {
		status := domain.ReplyRunCompleted
		errorText := ""
		if runErr != nil {
			status = domain.ReplyRunFailed
			errorText = runErr.Error()
		}
		return s.Repos.ReplyRuns.Finish(ctx, runID, status, errorText, now().UTC())
	}
	onInvocationStart := func(
		ctx context.Context,
		iteration int,
		call toolruntime.ToolCallBlock,
		_ bool,
	) (string, error) {
		definition := agenttools.Definition{}
		decisionJSON := agenttools.MarshalAuditDecision(false, definition, "tool_not_visible_in_current_room_policy")
		if registry != nil {
			resolved, ok, err := registry.LookupVisibleDefinition(
				agentToolSessionContext(roomContext),
				policy,
				call.Name,
			)
			if err != nil {
				return "", err
			}
			if ok {
				definition = resolved
				decisionJSON = agenttools.MarshalAuditDecision(true, definition, "visible_in_current_room_policy")
				if definition.Source == agenttools.SourceLocal {
					if decision, ok := toolruntime.DecisionForLocalToolCall(policy, call); ok {
						decisionJSON = agenttools.MarshalAuditDecision(decision.Allowed, definition, decision.Reason)
					}
				}
			}
		}
		invocationID := buildToolInvocationID(runID, iteration, call.ID, now)
		return invocationID, s.Repos.ToolInvocations.Insert(ctx, domain.ToolInvocation{
			ID:           invocationID,
			ReplyRunID:   runID,
			Iteration:    iteration,
			ToolCallID:   call.ID,
			ToolName:     call.Name,
			ToolSource:   string(definition.Source),
			Provider:     definition.Provider,
			CapabilityID: strings.TrimSpace(definition.CapabilityID),
			ArgsJSON:     normalizeAuditJSON(string(call.Args)),
			DecisionJSON: decisionJSON,
			Status:       domain.ToolInvocationRunning,
			StartedAt:    now().UTC(),
		})
	}
	onInvocationFinish := func(ctx context.Context, invocationID, content string, invocationErr error) error {
		status := domain.ToolInvocationCompleted
		errorText := ""
		if invocationErr != nil || toolruntime.ResultIsError(content) {
			status = domain.ToolInvocationFailed
		}
		if invocationErr != nil {
			errorText = invocationErr.Error()
		}
		return s.Repos.ToolInvocations.Finish(
			ctx,
			invocationID,
			status,
			normalizeAuditJSON(content),
			errorText,
			now().UTC(),
		)
	}
	return onRunStart, onRunFinish, onInvocationStart, onInvocationFinish
}

func buildReplyRunID(
	profileID domain.ProfileID,
	roomID domain.RoomID,
	messageID domain.MessageID,
	now func() time.Time,
) string {
	if strings.TrimSpace(string(messageID)) != "" {
		return fmt.Sprintf(
			"replyrun:%s:%s:%s",
			profileID,
			roomID,
			messageID,
		)
	}
	return fmt.Sprintf(
		"replyrun:%s:%s:%d",
		profileID,
		roomID,
		now().UTC().UnixNano(),
	)
}

func buildToolInvocationID(
	replyRunID string,
	iteration int,
	toolCallID string,
	now func() time.Time,
) string {
	if strings.TrimSpace(toolCallID) != "" {
		return fmt.Sprintf("toolinv:%s:%d:%s", replyRunID, iteration, toolCallID)
	}
	return fmt.Sprintf("toolinv:%s:%d:%d", replyRunID, iteration, now().UTC().UnixNano())
}

func normalizeAuditJSON(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "{}"
	}
	return trimmed
}
