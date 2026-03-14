package runtime

import (
	"fmt"
	"slices"
	"strings"

	"goclaw/internal/agenttools"
	"goclaw/internal/domain"
)

type toolingMode string

const (
	toolingModeDisabled toolingMode = "disabled"
	toolingModeLegacy   toolingMode = "legacy_json_fallback"
	toolingModeNative   toolingMode = "native"
)

type capabilitySnapshot struct {
	Channel      channelCapabilitySnapshot
	Tooling      toolingSnapshot
	Skills       skillsCapabilitySnapshot
	Memory       memoryCapabilitySnapshot
	Conversation conversationCapabilitySnapshot
	Runtime      runtimeCapabilitySnapshot
}

type channelCapabilitySnapshot struct {
	Provider            domain.Provider
	ProfileID           domain.ProfileID
	RoomKind            domain.RoomKind
	ReplyRouting        string
	ScopeSummary        string
	Hints               []string
	VisibleChannelTools []string
	VisibleClasses      []string
	MutationEnabled     bool
}

type toolingSnapshot struct {
	Mode         toolingMode
	Policy       toolPolicySnapshot
	VisibleTools []toolCapability
}

type toolCapability struct {
	Name         string
	Description  string
	Source       agenttools.Source
	Provider     domain.Provider
	SafetyClass  agenttools.SafetyClass
	CapabilityID string
}

type toolPolicySnapshot struct {
	CommandsMode             domain.ToolPermissionMode
	PathsMode                domain.ToolPermissionMode
	NetworkMode              domain.ToolPermissionMode
	ChannelIntrospectionMode domain.ToolPermissionMode
	ChannelReadMode          domain.ToolPermissionMode
	ChannelWriteMode         domain.ToolPermissionMode
	ChannelSensitiveMode     domain.ToolPermissionMode
	MCPIntrospectionMode     domain.ToolPermissionMode
	MCPReadMode              domain.ToolPermissionMode
	MCPWriteMode             domain.ToolPermissionMode
	MCPSensitiveMode         domain.ToolPermissionMode
}

type skillsCapabilitySnapshot struct {
	Mode          string
	Available     int
	Inline        int
	VisibleSkills []string
}

type memoryCapabilitySnapshot struct {
	MarkdownEnabled bool
	ProviderEnabled bool
	ReadScopes      []string
	WriteScopes     []string
	ConsentLevel    domain.ConsentAccessLevel
	JournalMode     domain.ConversationJournalMode
}

type conversationCapabilitySnapshot struct {
	Enabled bool
	ID      domain.ConversationID
	Slug    string
	Title   string
}

type runtimeCapabilitySnapshot struct {
	FinalOnlyDelivery bool
	WorkspaceRoot     string
	SkillsRoot        string
}

func buildCapabilitySnapshot(
	roomContext RoomContext,
	promptPackage PromptPackage,
	visibleDefinitions []agenttools.Definition,
	channelSummary agenttools.CapabilitySummary,
	promptBuilder *PromptBuilder,
	policy domain.ToolPermissionPolicy,
	mode toolingMode,
) capabilitySnapshot {
	settings := normalizeConversationSettings(roomContext.ConversationSettings, roomContext.Conversation.ID)
	readScopes := make([]string, 0, len(promptPackage.Retrieval.Scopes))
	for _, scope := range promptPackage.Retrieval.Scopes {
		readScopes = append(readScopes, string(scope.Kind))
	}
	writeScopes := append([]string(nil), settings.WriteScopes...)

	markdownEnabled := promptBuilder != nil &&
		promptBuilder.Workspace != nil &&
		promptBuilder.Workspace.Enabled() &&
		settings.AllowMarkdownMemory
	providerEnabled := promptBuilder != nil &&
		promptBuilder.Memory != nil &&
		promptBuilder.Memory.Name() != "noop" &&
		settings.AllowProviderMemory

	visibleTools := visibleToolCapabilities(visibleDefinitions)
	visibleChannelTools, visibleClasses, mutationEnabled := summarizeChannelTools(visibleDefinitions)

	return capabilitySnapshot{
		Channel: channelCapabilitySnapshot{
			Provider:            roomContext.Room.Provider,
			ProfileID:           roomContext.Profile.ID,
			RoomKind:            roomContext.Room.Kind,
			ReplyRouting:        "automatic_to_current_room",
			ScopeSummary:        normalizeCapabilitySummary(channelSummary.ScopeSummary),
			Hints:               channelHintLines(roomContext.Room.Provider, roomContext.Room.Kind),
			VisibleChannelTools: visibleChannelTools,
			VisibleClasses:      visibleClasses,
			MutationEnabled:     mutationEnabled,
		},
		Tooling: toolingSnapshot{
			Mode:         mode,
			Policy:       summarizeToolPolicy(policy),
			VisibleTools: visibleTools,
		},
		Skills: skillsCapabilitySnapshot{
			Mode:          skillModeLabel(promptPackage),
			Available:     len(promptPackage.Skills.AllSkills),
			Inline:        len(promptPackage.Skills.InlineSkills),
			VisibleSkills: skillNames(promptPackage.Skills.AllSkills),
		},
		Memory: memoryCapabilitySnapshot{
			MarkdownEnabled: markdownEnabled,
			ProviderEnabled: providerEnabled,
			ReadScopes:      readScopes,
			WriteScopes:     writeScopes,
			ConsentLevel:    roomContext.Consent.AccessLevel,
			JournalMode:     settings.JournalMode,
		},
		Conversation: conversationCapabilitySnapshot{
			Enabled: roomContext.Conversation.ID != "",
			ID:      roomContext.Conversation.ID,
			Slug:    roomContext.Conversation.Slug,
			Title:   roomContext.Conversation.Title,
		},
		Runtime: runtimeCapabilitySnapshot{
			FinalOnlyDelivery: true,
			WorkspaceRoot:     capabilityWorkspaceRoot(promptBuilder),
			SkillsRoot:        capabilitySkillsRoot(promptBuilder),
		},
	}
}

func appendCapabilityPrompt(system string, snapshot capabilitySnapshot) string {
	sections := []string{
		renderCapabilityOverview(snapshot),
		renderChannelGuidanceSection(snapshot.Channel),
		renderToolingSection(snapshot.Tooling),
		renderToolCallStyleSection(snapshot.Tooling),
		renderSkillsCapabilitySection(snapshot.Skills),
		renderMemoryCapabilitySection(snapshot.Memory),
		renderRuntimeLimitsSection(snapshot),
	}
	capabilityPrompt := strings.TrimSpace(strings.Join(filterNonEmpty(sections), "\n\n"))
	if capabilityPrompt == "" {
		return system
	}
	if strings.TrimSpace(system) == "" {
		return capabilityPrompt
	}
	return strings.TrimSpace(system) + "\n\n" + capabilityPrompt
}

func renderCapabilityOverview(snapshot capabilitySnapshot) string {
	memoryMode := "disabled"
	switch {
	case snapshot.Memory.MarkdownEnabled && snapshot.Memory.ProviderEnabled:
		memoryMode = "markdown+provider"
	case snapshot.Memory.MarkdownEnabled:
		memoryMode = "markdown"
	case snapshot.Memory.ProviderEnabled:
		memoryMode = "provider"
	}
	conversationState := "disabled"
	if snapshot.Conversation.Enabled {
		conversationState = "enabled"
	}
	lines := []string{"[Capabilities]"}
	if strings.TrimSpace(string(snapshot.Channel.Provider)) != "" {
		lines = append(lines, fmt.Sprintf("channel=%s", snapshot.Channel.Provider))
	}
	lines = append(lines,
		fmt.Sprintf("tools=%s", snapshot.Tooling.Mode),
		fmt.Sprintf("skills=%s", snapshot.Skills.Mode),
		fmt.Sprintf("memory=%s", memoryMode),
		fmt.Sprintf("personal_memory_access=%s", snapshot.Memory.ConsentLevel),
		"delivery=final_only",
		fmt.Sprintf("conversation=%s", conversationState),
	)
	return strings.Join(lines, "\n")
}

func renderChannelGuidanceSection(snapshot channelCapabilitySnapshot) string {
	if strings.TrimSpace(string(snapshot.Provider)) == "" {
		return ""
	}

	lines := []string{
		"[Channel Guidance]",
		fmt.Sprintf("provider=%s", snapshot.Provider),
		fmt.Sprintf("profile_id=%s", snapshot.ProfileID),
		fmt.Sprintf("room_kind=%s", snapshot.RoomKind),
		fmt.Sprintf("reply_routing=%s", snapshot.ReplyRouting),
	}
	if len(snapshot.VisibleClasses) > 0 {
		lines = append(lines, fmt.Sprintf("channel_tools=%s", strings.Join(snapshot.VisibleClasses, ",")))
	}
	if len(snapshot.VisibleChannelTools) > 0 {
		lines = append(lines, fmt.Sprintf("channel_tool_names=%s", strings.Join(snapshot.VisibleChannelTools, ",")))
	}
	if strings.TrimSpace(snapshot.ScopeSummary) != "" {
		lines = append(lines, fmt.Sprintf("channel_scope_summary=%s", snapshot.ScopeSummary))
	}
	if snapshot.MutationEnabled {
		lines = append(lines, "channel_mutations=enabled")
	} else {
		lines = append(lines, "channel_mutations=disabled")
	}
	for _, hint := range snapshot.Hints {
		trimmed := strings.TrimSpace(hint)
		if trimmed == "" {
			continue
		}
		lines = append(lines, "- "+trimmed)
	}
	return strings.Join(lines, "\n")
}

func renderToolingSection(snapshot toolingSnapshot) string {
	lines := []string{
		"[Tooling]",
		fmt.Sprintf(
			"local_policy=commands:%s,paths:%s,network:%s",
			snapshot.Policy.CommandsMode,
			snapshot.Policy.PathsMode,
			snapshot.Policy.NetworkMode,
		),
		fmt.Sprintf(
			"channel_policy=introspection:%s,read:%s,write:%s,sensitive:%s",
			snapshot.Policy.ChannelIntrospectionMode,
			snapshot.Policy.ChannelReadMode,
			snapshot.Policy.ChannelWriteMode,
			snapshot.Policy.ChannelSensitiveMode,
		),
		fmt.Sprintf(
			"mcp_policy=introspection:%s,read:%s,write:%s,sensitive:%s",
			snapshot.Policy.MCPIntrospectionMode,
			snapshot.Policy.MCPReadMode,
			snapshot.Policy.MCPWriteMode,
			snapshot.Policy.MCPSensitiveMode,
		),
	}
	if len(snapshot.VisibleTools) == 0 {
		lines = append(lines, "No room-visible tools are currently exposed.")
		return strings.Join(lines, "\n")
	}
	for _, tool := range snapshot.VisibleTools {
		parts := []string{tool.Name + ":"}
		if strings.TrimSpace(tool.Description) != "" {
			parts = append(parts, tool.Description)
		}
		meta := make([]string, 0, 3)
		if strings.TrimSpace(string(tool.Source)) != "" {
			meta = append(meta, "source="+string(tool.Source))
		}
		if strings.TrimSpace(string(tool.Provider)) != "" {
			meta = append(meta, "provider="+string(tool.Provider))
		}
		if strings.TrimSpace(string(tool.SafetyClass)) != "" {
			meta = append(meta, "safety="+string(tool.SafetyClass))
		}
		if len(meta) > 0 {
			parts = append(parts, "("+strings.Join(meta, ", ")+")")
		}
		lines = append(lines, "- "+strings.Join(parts, " "))
	}
	return strings.Join(lines, "\n")
}

func renderToolCallStyleSection(snapshot toolingSnapshot) string {
	if snapshot.Mode == toolingModeDisabled {
		return ""
	}
	return strings.Join([]string{
		"[Tool Call Style]",
		"- Prefer the least powerful visible tool that solves the task.",
		"- Never invent tool results.",
		"- After a tool result, either request one more visible tool or answer normally.",
	}, "\n")
}

func renderMemoryCapabilitySection(snapshot memoryCapabilitySnapshot) string {
	readScopes := "none"
	if len(snapshot.ReadScopes) > 0 {
		readScopes = strings.Join(snapshot.ReadScopes, ",")
	}
	writeScopes := "none"
	if len(snapshot.WriteScopes) > 0 {
		writeScopes = strings.Join(snapshot.WriteScopes, ",")
	}
	return strings.Join([]string{
		"[Memory]",
		fmt.Sprintf("read_scopes=%s", readScopes),
		fmt.Sprintf("write_scopes=%s", writeScopes),
		fmt.Sprintf("markdown_memory=%s", enabledLabel(snapshot.MarkdownEnabled)),
		fmt.Sprintf("provider_memory=%s", enabledLabel(snapshot.ProviderEnabled)),
		fmt.Sprintf("journal_mode=%s", snapshot.JournalMode),
	}, "\n")
}

func renderSkillsCapabilitySection(snapshot skillsCapabilitySnapshot) string {
	if snapshot.Mode == "disabled" {
		return ""
	}
	lines := []string{
		"[Skill Capabilities]",
		fmt.Sprintf("mode=%s", snapshot.Mode),
		fmt.Sprintf("available=%d", snapshot.Available),
		fmt.Sprintf("inline=%d", snapshot.Inline),
		"skills_are_prompt_guidance_only=true",
	}
	if len(snapshot.VisibleSkills) > 0 {
		lines = append(lines, "skill_names="+strings.Join(snapshot.VisibleSkills, ","))
	}
	return strings.Join(lines, "\n")
}

func renderRuntimeLimitsSection(snapshot capabilitySnapshot) string {
	lines := []string{
		"[Runtime Limits]",
		"external_delivery=final_only",
		"tool_visibility_is_policy_filtered=true",
		"TOOLS.md_is_guidance_only=true",
	}
	if strings.TrimSpace(snapshot.Runtime.WorkspaceRoot) != "" {
		lines = append(lines, fmt.Sprintf("workspace_root=%s", snapshot.Runtime.WorkspaceRoot))
	}
	if strings.TrimSpace(snapshot.Runtime.SkillsRoot) != "" {
		lines = append(lines, fmt.Sprintf("skills_root=%s", snapshot.Runtime.SkillsRoot))
	}
	return strings.Join(lines, "\n")
}

func visibleToolCapabilities(definitions []agenttools.Definition) []toolCapability {
	if len(definitions) == 0 {
		return nil
	}
	tools := make([]toolCapability, 0, len(definitions))
	for _, definition := range definitions {
		tools = append(tools, toolCapability{
			Name:         definition.Name,
			Description:  definition.Description,
			Source:       definition.Source,
			Provider:     definition.Provider,
			SafetyClass:  definition.SafetyClass,
			CapabilityID: definition.CapabilityID,
		})
	}
	return tools
}

func summarizeToolPolicy(policy domain.ToolPermissionPolicy) toolPolicySnapshot {
	return toolPolicySnapshot{
		CommandsMode:             normalizeToolPermissionMode(policy.CommandsMode),
		PathsMode:                normalizeToolPermissionMode(policy.PathsMode),
		NetworkMode:              normalizeToolPermissionMode(policy.NetworkMode),
		ChannelIntrospectionMode: normalizeToolPermissionMode(policy.ChannelIntrospectionMode),
		ChannelReadMode:          normalizeToolPermissionMode(policy.ChannelReadMode),
		ChannelWriteMode:         normalizeToolPermissionMode(policy.ChannelWriteMode),
		ChannelSensitiveMode:     normalizeToolPermissionMode(policy.ChannelSensitiveMode),
		MCPIntrospectionMode:     normalizeToolPermissionMode(policy.MCPIntrospectionMode),
		MCPReadMode:              normalizeToolPermissionMode(policy.MCPReadMode),
		MCPWriteMode:             normalizeToolPermissionMode(policy.MCPWriteMode),
		MCPSensitiveMode:         normalizeToolPermissionMode(policy.MCPSensitiveMode),
	}
}

func capabilityWorkspaceRoot(promptBuilder *PromptBuilder) string {
	if promptBuilder == nil || promptBuilder.Workspace == nil || !promptBuilder.Workspace.Enabled() {
		return ""
	}
	return strings.TrimSpace(promptBuilder.Workspace.Root)
}

func capabilitySkillsRoot(promptBuilder *PromptBuilder) string {
	if promptBuilder == nil || promptBuilder.Skills == nil || !promptBuilder.Skills.Enabled() {
		return ""
	}
	return strings.TrimSpace(promptBuilder.Skills.Root)
}

func enabledLabel(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

func normalizeToolPermissionMode(mode domain.ToolPermissionMode) domain.ToolPermissionMode {
	switch mode {
	case domain.ToolPermissionAllowAll, domain.ToolPermissionAllowList:
		return mode
	default:
		return domain.ToolPermissionDenyAll
	}
}

func normalizeCapabilitySummary(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	replacer := strings.NewReplacer(", ", "_", ",", "_", " ", "_")
	return replacer.Replace(trimmed)
}

func channelHintLines(provider domain.Provider, roomKind domain.RoomKind) []string {
	switch provider {
	case domain.ProviderFeishu:
		lines := []string{
			"You are currently replying inside Feishu.",
			"A normal assistant reply is delivered back to this same Feishu conversation automatically.",
			"Treat `chat_id` as the room identifier and `open_id` as the user identifier for this channel.",
			"The current Feishu reply path sends normal assistant output as plain text.",
		}
		if roomKind == domain.RoomKindDirect {
			return append(lines, "This is a direct chat with one user.")
		}
		return append(lines, "This is a shared group chat, so other room members can see your reply.")
	default:
		if strings.TrimSpace(string(provider)) == "" {
			return nil
		}
		return []string{
			fmt.Sprintf("You are currently replying through the %s channel.", provider),
			"A normal assistant reply is delivered back to the current conversation automatically.",
		}
	}
}

func summarizeChannelTools(definitions []agenttools.Definition) ([]string, []string, bool) {
	names := make([]string, 0)
	classSet := make(map[string]struct{})
	mutationEnabled := false
	for _, definition := range definitions {
		if definition.Source != agenttools.SourceChannel {
			continue
		}
		names = append(names, definition.Name)
		if definition.SafetyClass != "" {
			classSet[string(definition.SafetyClass)] = struct{}{}
		}
		if definition.SafetyClass == agenttools.SafetyMutating || definition.SafetyClass == agenttools.SafetySensitive {
			mutationEnabled = true
		}
	}
	slices.Sort(names)
	classes := make([]string, 0, len(classSet))
	for value := range classSet {
		classes = append(classes, value)
	}
	slices.Sort(classes)
	return names, classes, mutationEnabled
}

func skillModeLabel(promptPackage PromptPackage) string {
	if len(promptPackage.Skills.AllSkills) == 0 {
		return "disabled"
	}
	return "prompt_only"
}

func skillNames(items []skillPromptBlock) []string {
	names := make([]string, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.Name) == "" {
			continue
		}
		names = append(names, item.Name)
	}
	slices.Sort(names)
	return names
}

func filterNonEmpty(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item) == "" {
			continue
		}
		out = append(out, item)
	}
	return out
}
