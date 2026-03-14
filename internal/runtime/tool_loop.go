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

type replyModelPlan struct {
	Request  model.Request
	Policy   domain.ToolPermissionPolicy
	Mode     toolingMode
	Session  agenttools.SessionContext
	Registry *agenttools.Registry
}

func (s *ReplyService) generateReplyText(
	ctx context.Context,
	roomContext RoomContext,
	promptPackage PromptPackage,
) (string, error) {
	snapshot, err := s.buildRunSnapshot(ctx, roomContext, promptPackage)
	if err != nil {
		return "", err
	}
	return s.generateReplyTextFromSnapshot(ctx, snapshot)
}

func (s *ReplyService) buildReplyModelPlan(
	ctx context.Context,
	roomContext RoomContext,
	promptPackage PromptPackage,
) (replyModelPlan, error) {
	request := toModelRequest(promptPackage)
	policy, enabled, err := s.resolveToolLoopPolicy(ctx, roomContext)
	if err != nil {
		return replyModelPlan{}, err
	}
	session := agentToolSessionContext(roomContext)
	visibleTools, toolDefinitions, err := s.visibleAgentTools(session, policy)
	if err != nil {
		return replyModelPlan{}, err
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
	return replyModelPlan{
		Request:  request,
		Policy:   policy,
		Mode:     mode,
		Session:  session,
		Registry: registry,
	}, nil
}

func (s *ReplyService) generateReplyTextFromPlan(
	ctx context.Context,
	roomContext RoomContext,
	plan replyModelPlan,
) (string, error) {
	return s.generateReplyTextFromSnapshot(ctx, freezeRunSnapshot(
		buildReplyRunID(roomContext.Profile.ID, roomContext.Room.ID, roomContext.TriggerMessageID, s.Now),
		runEventNow(s.Now),
		roomContext,
		PromptPackage{},
		plan,
	))
}

func (s *ReplyService) generateReplyTextFromSnapshot(
	ctx context.Context,
	snapshot RunSnapshot,
) (string, error) {
	return s.generateReplyTextFromSnapshotWithObservers(ctx, snapshot, nil)
}

func (s *ReplyService) generateReplyTextFromSnapshotWithObservers(
	ctx context.Context,
	snapshot RunSnapshot,
	required []RunObserver,
) (string, error) {
	plan := snapshot.ReplyPlan
	runEvents := newRunEventEmitterForSnapshot(s.Logger, snapshot, s.Now, required, s.RunObservers)
	request := plan.Request
	policy := snapshot.ToolPolicy
	mode := snapshot.ToolingMode
	session := plan.Session
	registry := plan.Registry
	if mode == toolingModeDisabled || registry == nil {
		if err := runEvents.EmitRunStarted(ctx); err != nil {
			return "", err
		}
		finalText, err := s.Model.Generate(ctx, request)
		if finishErr := runEvents.EmitRunFinished(ctx, finalText, err); finishErr != nil && err == nil {
			return "", finishErr
		}
		return finalText, err
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
				"profile_id", snapshot.Profile.ID,
				"room_id", snapshot.Room.ID,
				"tool", call.Name,
				"iteration", iteration,
			)
		},
	}
	auditRunStart, auditRunFinish, auditInvocationStart, auditInvocationFinish :=
		s.toolingAuditHooks(snapshot.RoomContext(), policy, mode)
	orchestrator.OnRunStart = func(runCtx context.Context) error {
		if err := runEvents.EmitRunStarted(runCtx); err != nil {
			return err
		}
		if auditRunStart != nil {
			return auditRunStart(runCtx)
		}
		return nil
	}
	orchestrator.OnRunFinish = func(runCtx context.Context, finalText string, runErr error) error {
		if auditRunFinish != nil {
			if err := auditRunFinish(runCtx, finalText, runErr); err != nil && runErr == nil {
				runErr = err
			}
		}
		return runEvents.EmitRunFinished(runCtx, finalText, runErr)
	}
	orchestrator.OnInvocationStart = func(
		runCtx context.Context,
		iteration int,
		call toolruntime.ToolCallBlock,
		native bool,
	) (string, error) {
		if err := runEvents.OnToolCallStarted(
			runCtx,
			iteration,
			call.ID,
			call.Name,
			string(call.Args),
			toolruntime.ToolCallSummary(call),
			native,
		); err != nil {
			return "", err
		}
		if auditInvocationStart != nil {
			return auditInvocationStart(runCtx, iteration, call, native)
		}
		return "", nil
	}
	orchestrator.OnInvocationFinish = func(
		runCtx context.Context,
		invocationID string,
		content string,
		invocationErr error,
	) error {
		if auditInvocationFinish != nil {
			if err := auditInvocationFinish(runCtx, invocationID, content, invocationErr); err != nil && invocationErr == nil {
				invocationErr = err
			}
		}
		return runEvents.OnToolCallFinished(runCtx, content, invocationErr)
	}
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
	if policy.MCPIntrospectionMode != domain.ToolPermissionDenyAll ||
		policy.MCPReadMode != domain.ToolPermissionDenyAll ||
		policy.MCPWriteMode != domain.ToolPermissionDenyAll ||
		policy.MCPSensitiveMode != domain.ToolPermissionDenyAll {
		return true
	}
	if len(policy.AllowedChannelProviders) > 0 ||
		len(policy.DeniedChannelProviders) > 0 ||
		len(policy.AllowedChannelTools) > 0 ||
		len(policy.DeniedChannelTools) > 0 {
		return true
	}
	if len(policy.AllowedMCPServers) > 0 ||
		len(policy.DeniedMCPServers) > 0 ||
		len(policy.AllowedMCPTools) > 0 ||
		len(policy.DeniedMCPTools) > 0 {
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
		fmt.Sprintf("mcp_introspection_mode=%s", toolPermissionModeLabel(policy.MCPIntrospectionMode)),
		fmt.Sprintf("mcp_read_mode=%s", toolPermissionModeLabel(policy.MCPReadMode)),
		fmt.Sprintf("mcp_write_mode=%s", toolPermissionModeLabel(policy.MCPWriteMode)),
		fmt.Sprintf("mcp_sensitive_mode=%s", toolPermissionModeLabel(policy.MCPSensitiveMode)),
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
	if len(policy.AllowedMCPTools) > 0 {
		lines = append(lines, "allowed_mcp_tools="+strings.Join(policy.AllowedMCPTools, ","))
	}
	if len(policy.DeniedMCPTools) > 0 {
		lines = append(lines, "denied_mcp_tools="+strings.Join(policy.DeniedMCPTools, ","))
	}
	if len(policy.AllowedMCPServers) > 0 {
		lines = append(lines, "allowed_mcp_servers="+strings.Join(policy.AllowedMCPServers, ","))
	}
	if len(policy.DeniedMCPServers) > 0 {
		lines = append(lines, "denied_mcp_servers="+strings.Join(policy.DeniedMCPServers, ","))
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
