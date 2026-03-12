package runtime

import (
	"context"
	"fmt"
	"strings"

	"goclaw/internal/agenttools"
	"goclaw/internal/config"
	"goclaw/internal/domain"
	"goclaw/internal/model"
	toolruntime "goclaw/internal/tools"
)

func (s *ReplyService) generateReplyText(
	ctx context.Context,
	roomContext RoomContext,
	promptPackage PromptPackage,
) (string, error) {
	request := toModelRequest(promptPackage)
	policy, enabled, err := s.resolveToolLoopPolicy(ctx, roomContext)
	if err != nil {
		return "", err
	}
	session := agentToolSessionContext(roomContext)
	visibleTools, toolDefinitions, err := s.visibleAgentTools(session, policy)
	if err != nil {
		return "", err
	}
	mode := s.resolveToolingMode(enabled && len(visibleTools) > 0)
	registry := s.toolRegistry()
	channelSummary := agenttools.CapabilitySummary{}
	if registry != nil {
		summary, summaryErr := registry.ChannelCapabilitySummary(ctx, session, policy)
		if summaryErr != nil {
			s.Logger.Warn(
				"runtime: failed to resolve channel capability summary",
				"profile_id", roomContext.Profile.ID,
				"room_id", roomContext.Room.ID,
				"provider", roomContext.Room.Provider,
				"error", summaryErr,
			)
		} else {
			channelSummary = summary
		}
	}
	request.System = appendCapabilityPrompt(
		request.System,
		buildCapabilitySnapshot(roomContext, promptPackage, toolDefinitions, channelSummary, s.PromptBuilder, policy, mode),
	)
	if mode == toolingModeDisabled || registry == nil {
		return s.Model.Generate(ctx, request)
	}
	orchestrator := toolruntime.Orchestrator{
		Registry: registry,
		Session:  session,
		Execution: agenttools.ExecutionContext{
			Session: session,
			Logger:  s.Logger,
			Repos:   s.Repos,
			Now:     s.Now,
		},
		Policy:             policy,
		MaxIterations:      s.toolMaxIterations(),
		AppendLegacyPrompt: appendToolInstructions,
		AppendNativePrompt: appendNativeToolContext,
		ObserveExecution: func(iteration int, call toolruntime.ToolCallBlock, native bool) {
			message := "runtime: executed guarded tool call"
			if native {
				message = "runtime: executed guarded native tool call"
			}
			s.Logger.Info(
				message,
				"profile_id", roomContext.Profile.ID,
				"room_id", roomContext.Room.ID,
				"tool", call.Name,
				"iteration", iteration,
			)
		},
	}
	orchestrator.OnRunStart, orchestrator.OnRunFinish, orchestrator.OnInvocationStart, orchestrator.OnInvocationFinish =
		s.toolingAuditHooks(roomContext, policy, mode)
	return orchestrator.Run(ctx, s.Model, request)
}

func (s *ReplyService) toolMaxIterations() int {
	if s.ToolMaxIterations > 0 {
		return s.ToolMaxIterations
	}
	return config.Default().Tooling.MaxIterations
}

func (s *ReplyService) resolveToolingMode(enabled bool) toolingMode {
	if !enabled || s.Tools == nil {
		return toolingModeDisabled
	}
	if _, ok := model.NativeToolProviderFor(s.Model); ok {
		return toolingModeNative
	}
	if s.LegacyToolFallbackEnabled {
		return toolingModeLegacy
	}
	return toolingModeDisabled
}

func (s *ReplyService) resolveToolLoopPolicy(
	ctx context.Context,
	roomContext RoomContext,
) (domain.ToolPermissionPolicy, bool, error) {
	if s.toolRegistry() == nil {
		return domain.ToolPermissionPolicy{}, false, nil
	}
	policy, err := s.Repos.ToolPermissions.Resolve(ctx, roomContext.Profile.ID, roomContext.Room.ID)
	if err != nil {
		return domain.ToolPermissionPolicy{}, false, err
	}
	return policy, toolLoopEnabled(policy), nil
}

func toolLoopEnabled(policy domain.ToolPermissionPolicy) bool {
	if policy.ChannelIntrospectionMode != domain.ToolPermissionDenyAll ||
		policy.ChannelReadMode != domain.ToolPermissionDenyAll ||
		policy.ChannelWriteMode != domain.ToolPermissionDenyAll ||
		policy.ChannelSensitiveMode != domain.ToolPermissionDenyAll {
		return true
	}
	if len(policy.AllowedChannelProviders) > 0 ||
		len(policy.DeniedChannelProviders) > 0 ||
		len(policy.AllowedChannelTools) > 0 ||
		len(policy.DeniedChannelTools) > 0 {
		return true
	}
	if policy.CommandsMode != domain.ToolPermissionDenyAll {
		return true
	}
	if policy.PathsMode != domain.ToolPermissionDenyAll {
		return true
	}
	if policy.NetworkMode != domain.ToolPermissionDenyAll {
		return true
	}
	return len(policy.AllowedCommands) > 0 ||
		len(policy.DeniedCommands) > 0 ||
		len(policy.AllowedPaths) > 0 ||
		len(policy.DeniedPaths) > 0 ||
		len(policy.AllowedHosts) > 0 ||
		len(policy.DeniedHosts) > 0
}

func (s *ReplyService) visibleAgentTools(
	session agenttools.SessionContext,
	policy domain.ToolPermissionPolicy,
) ([]model.Tool, []agenttools.Definition, error) {
	registry := s.toolRegistry()
	if registry == nil {
		return nil, nil, nil
	}
	return registry.VisibleModelTools(session, policy)
}

func (s *ReplyService) toolRegistry() *agenttools.Registry {
	if s.AgentTools != nil {
		return s.AgentTools
	}
	if s.Tools == nil {
		return nil
	}
	return agenttools.NewRegistry(toolruntime.NewLocalToolProvider(s.Tools))
}

func agentToolSessionContext(roomContext RoomContext) agenttools.SessionContext {
	return agenttools.SessionContext{
		Provider:                 roomContext.Room.Provider,
		ProfileID:                roomContext.Profile.ID,
		RoomID:                   roomContext.Room.ID,
		ProviderRoomID:           roomContext.Room.ProviderRoomID,
		RoomKind:                 roomContext.Room.Kind,
		PersonID:                 roomContext.Person.ID,
		ProviderUserID:           roomContext.Person.ProviderUserID,
		ConversationID:           roomContext.Conversation.ID,
		CurrentMessageProviderID: strings.TrimSpace(roomContext.TriggerProviderMessageID),
	}
}

func appendToolInstructions(system string, policy domain.ToolPermissionPolicy) string {
	instruction := fmt.Sprintf(
		"[Tools]\n"+
			"You may answer normally, or request one tool by returning ONLY JSON of the form:\n"+
			"{\"type\":\"tool_call\",\"tool\":\"exec|read|write|fetch\",\"args\":{...}}\n"+
			"Supported tool args:\n"+
			"- exec: {\"program\":\"cmd\",\"args\":[...],\"workdir\":\"/path\",\"stdin\":\"...\",\"timeout\":\"10s\"}\n"+
			"- read: {\"path\":\"/path/to/file\"}\n"+
			"- write: {\"path\":\"/path/to/file\",\"content\":\"...\",\"append\":false,\"mkdir\":false}\n"+
			"- fetch: {\"url\":\"https://...\",\"timeout\":\"10s\",\"max_bytes\":65536}\n"+
			"Do not invent tool results. After you receive a tool result, either answer normally or request one more tool.\n"+
			"Current room tool policy:\n%s",
		renderToolPolicy(policy),
	)
	if strings.TrimSpace(system) == "" {
		return instruction
	}
	return strings.TrimSpace(system) + "\n\n" + instruction
}

func appendNativeToolContext(system string, policy domain.ToolPermissionPolicy) string {
	instruction := fmt.Sprintf(
		"[Tools]\n"+
			"You may use the provided native tools when necessary. Respect the current room tool policy and prefer the least powerful tool that solves the task.\n"+
			"Current room tool policy:\n%s",
		renderToolPolicy(policy),
	)
	if strings.TrimSpace(system) == "" {
		return instruction
	}
	return strings.TrimSpace(system) + "\n\n" + instruction
}

func renderToolPolicy(policy domain.ToolPermissionPolicy) string {
	lines := []string{
		fmt.Sprintf("commands_mode=%s", toolPermissionModeLabel(policy.CommandsMode)),
		fmt.Sprintf("paths_mode=%s", toolPermissionModeLabel(policy.PathsMode)),
		fmt.Sprintf("network_mode=%s", toolPermissionModeLabel(policy.NetworkMode)),
		fmt.Sprintf("channel_introspection_mode=%s", toolPermissionModeLabel(policy.ChannelIntrospectionMode)),
		fmt.Sprintf("channel_read_mode=%s", toolPermissionModeLabel(policy.ChannelReadMode)),
		fmt.Sprintf("channel_write_mode=%s", toolPermissionModeLabel(policy.ChannelWriteMode)),
		fmt.Sprintf("channel_sensitive_mode=%s", toolPermissionModeLabel(policy.ChannelSensitiveMode)),
	}
	if len(policy.AllowedCommands) > 0 {
		lines = append(lines, "allowed_commands="+strings.Join(policy.AllowedCommands, ","))
	}
	if len(policy.DeniedCommands) > 0 {
		lines = append(lines, "denied_commands="+strings.Join(policy.DeniedCommands, ","))
	}
	if len(policy.AllowedPaths) > 0 {
		lines = append(lines, "allowed_paths="+strings.Join(policy.AllowedPaths, ","))
	}
	if len(policy.DeniedPaths) > 0 {
		lines = append(lines, "denied_paths="+strings.Join(policy.DeniedPaths, ","))
	}
	if len(policy.AllowedHosts) > 0 {
		lines = append(lines, "allowed_hosts="+strings.Join(policy.AllowedHosts, ","))
	}
	if len(policy.DeniedHosts) > 0 {
		lines = append(lines, "denied_hosts="+strings.Join(policy.DeniedHosts, ","))
	}
	if len(policy.AllowedChannelTools) > 0 {
		lines = append(lines, "allowed_channel_tools="+strings.Join(policy.AllowedChannelTools, ","))
	}
	if len(policy.DeniedChannelTools) > 0 {
		lines = append(lines, "denied_channel_tools="+strings.Join(policy.DeniedChannelTools, ","))
	}
	if len(policy.AllowedChannelProviders) > 0 {
		lines = append(lines, "allowed_channel_providers="+strings.Join(policy.AllowedChannelProviders, ","))
	}
	if len(policy.DeniedChannelProviders) > 0 {
		lines = append(lines, "denied_channel_providers="+strings.Join(policy.DeniedChannelProviders, ","))
	}
	return strings.Join(lines, "\n")
}

func toolPermissionModeLabel(mode domain.ToolPermissionMode) domain.ToolPermissionMode {
	switch mode {
	case domain.ToolPermissionAllowAll, domain.ToolPermissionAllowList:
		return mode
	default:
		return domain.ToolPermissionDenyAll
	}
}
