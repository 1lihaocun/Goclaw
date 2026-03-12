package feishuchannel

import (
	"slices"
	"strings"

	rawfeishu "goclaw/internal/feishu"
)

type CapabilityRequirement struct {
	AllOfScopes                []string `json:"all_of_scopes,omitempty"`
	AnyOfScopes                []string `json:"any_of_scopes,omitempty"`
	RequiredEventSubscriptions []string `json:"required_event_subscriptions,omitempty"`
}

type CapabilityStatus struct {
	ID            string                `json:"id"`
	Title         string                `json:"title"`
	Description   string                `json:"description"`
	CodeSupported bool                  `json:"code_supported"`
	Enabled       bool                  `json:"enabled"`
	Requirements  CapabilityRequirement `json:"requirements"`
	MissingScopes []string              `json:"missing_scopes,omitempty"`
	Notes         []string              `json:"notes,omitempty"`
}

type CapabilityReport struct {
	Provider       string             `json:"provider"`
	AccountID      string             `json:"account_id,omitempty"`
	ProfileID      string             `json:"profile_id,omitempty"`
	ConnectionMode string             `json:"connection_mode,omitempty"`
	ScopeSummary   string             `json:"scope_summary"`
	Capabilities   []CapabilityStatus `json:"capabilities"`
}

type capabilitySpec struct {
	ID          string
	Title       string
	Description string
	AllOfScopes []string
	AnyOfScopes []string
	Events      []string
	Notes       []string
}

var feishuCapabilitySpecs = []capabilitySpec{
	{
		ID:          "im.receive_p2p_messages",
		Title:       "Receive P2P Messages",
		Description: "Receive direct messages from users in private chats.",
		AllOfScopes: []string{"im:message.p2p_msg:readonly"},
		Events:      []string{"im.message.receive_v1"},
	},
	{
		ID:          "im.receive_group_messages",
		Title:       "Receive Group Messages",
		Description: "Receive full group chat messages when the bot is in the room.",
		AllOfScopes: []string{"im:message.group_msg"},
		Events:      []string{"im.message.receive_v1"},
	},
	{
		ID:          "im.receive_group_mentions",
		Title:       "Receive Group Mentions",
		Description: "Receive group messages that explicitly @mention the bot.",
		AllOfScopes: []string{"im:message.group_at_msg:readonly"},
		Events:      []string{"im.message.receive_v1"},
	},
	{
		ID:          "im.send_text_messages",
		Title:       "Send Bot Messages",
		Description: "Send text replies into Feishu chats as the bot.",
		AllOfScopes: []string{"im:message:send_as_bot"},
	},
	{
		ID:          "im.send_post_messages",
		Title:       "Send Rich Post Messages",
		Description: "Send Feishu rich-text post messages into chats as the bot.",
		AllOfScopes: []string{"im:message:send_as_bot"},
	},
	{
		ID:          "im.send_interactive_messages",
		Title:       "Send Interactive Card Messages",
		Description: "Send Feishu interactive card messages through the bot send API.",
		AllOfScopes: []string{"im:message:send_as_bot"},
	},
	{
		ID:          "im.reply_messages",
		Title:       "Reply To Messages",
		Description: "Reply to Feishu messages as the bot across supported message types.",
		AllOfScopes: []string{"im:message:send_as_bot"},
	},
	{
		ID:          "im.forward_messages",
		Title:       "Forward Messages",
		Description: "Forward single messages to rooms, users, or threads as the bot.",
		AllOfScopes: []string{"im:message:send_as_bot"},
	},
	{
		ID:          "im.merge_forward_messages",
		Title:       "Merge Forward Messages",
		Description: "Merge-forward multiple messages to a target room, user, or thread as the bot.",
		AllOfScopes: []string{"im:message:send_as_bot"},
	},
	{
		ID:          "im.forward_threads",
		Title:       "Forward Threads",
		Description: "Forward Feishu threads to a target room, user, or thread as the bot.",
		AllOfScopes: []string{"im:message:send_as_bot"},
	},
	{
		ID:          "im.push_follow_up_messages",
		Title:       "Push Follow Ups",
		Description: "Push follow-up text snippets onto existing Feishu bot messages.",
		AllOfScopes: []string{"im:message:send_as_bot"},
	},
	{
		ID:          "im.urgent_app_messages",
		Title:       "In-App Urgent Reminders",
		Description: "Escalate a bot message with Feishu in-app urgent delivery.",
		AllOfScopes: []string{"im:message:send_as_bot"},
	},
	{
		ID:          "im.urgent_phone_messages",
		Title:       "Phone Urgent Reminders",
		Description: "Escalate a bot message with Feishu phone urgent delivery.",
		AllOfScopes: []string{"im:message:send_as_bot"},
	},
	{
		ID:          "im.urgent_sms_messages",
		Title:       "SMS Urgent Reminders",
		Description: "Escalate a bot message with Feishu SMS urgent delivery.",
		AllOfScopes: []string{"im:message:send_as_bot"},
	},
	{
		ID:          "im.read_chat_metadata",
		Title:       "Read Chat Metadata",
		Description: "Read chat metadata and chat member lists through Feishu IM APIs.",
		AnyOfScopes: []string{"im:chat:read", "im:chat:readonly"},
	},
	{
		ID:          "im.update_messages",
		Title:       "Update Messages",
		Description: "Edit existing bot messages.",
		AllOfScopes: []string{"im:message:update"},
	},
	{
		ID:          "im.recall_messages",
		Title:       "Recall Messages",
		Description: "Recall previously sent bot messages.",
		AllOfScopes: []string{"im:message:recall"},
	},
	{
		ID:          "im.read_reactions",
		Title:       "Read Reactions",
		Description: "List message reactions via IM APIs.",
		AllOfScopes: []string{"im:message.reactions:read"},
	},
	{
		ID:          "im.write_reactions",
		Title:       "Write Reactions",
		Description: "Add and delete message reactions.",
		AllOfScopes: []string{"im:message.reactions:write_only"},
	},
	{
		ID:          "im.read_pins",
		Title:       "Read Pins",
		Description: "List pinned messages in Feishu chats.",
		AllOfScopes: []string{"im:message.pins:read"},
	},
	{
		ID:          "im.write_pins",
		Title:       "Write Pins",
		Description: "Pin and unpin messages in Feishu chats.",
		AllOfScopes: []string{"im:message.pins:write_only"},
	},
	{
		ID:          "docs.read_documents",
		Title:       "Read Feishu Documents",
		Description: "Read raw document content from Feishu doc/docx APIs.",
		AnyOfScopes: []string{"docx:document:readonly", "docs:doc:readonly"},
	},
	{
		ID:          "wiki.resolve_nodes",
		Title:       "Resolve Feishu Wiki Nodes",
		Description: "Resolve Feishu wiki nodes to their backing document tokens before reading content.",
		AllOfScopes: []string{"wiki:node:read"},
		AnyOfScopes: []string{"wiki:wiki:readonly", "wiki:wiki"},
	},
	{
		ID:          "im.recall_event_sync",
		Title:       "Recall Event Sync",
		Description: "Persist message recall events and sync local transcript state to [recalled].",
		Events:      []string{"im.message.recalled_v1"},
		Notes: []string{
			"Scope grant is not queried separately here; event subscription still needs to be enabled in Feishu.",
		},
	},
	{
		ID:          "im.reaction_event_sync",
		Title:       "Reaction Event Sync",
		Description: "Persist reaction create/delete events for later automation or auditing.",
		Events:      []string{"im.message.reaction.created_v1", "im.message.reaction.deleted_v1"},
	},
	{
		ID:          "im.chat_member_event_sync",
		Title:       "Chat Member Event Sync",
		Description: "Persist bot/user membership change events for rooms.",
		Events: []string{
			"im.chat.member.bot.added_v1",
			"im.chat.member.bot.deleted_v1",
			"im.chat.member.user.added_v1",
			"im.chat.member.user.deleted_v1",
			"im.chat.member.user.withdrawn_v1",
		},
	},
}

func BuildCapabilityReport(account Account, scopes rawfeishu.AppScopes) CapabilityReport {
	granted := make(map[string]struct{}, len(scopes.Granted))
	for _, scope := range scopes.Granted {
		name := strings.TrimSpace(scope.Name)
		if name == "" {
			continue
		}
		granted[name] = struct{}{}
	}

	capabilities := make([]CapabilityStatus, 0, len(feishuCapabilitySpecs))
	for _, spec := range feishuCapabilitySpecs {
		missingAll := missingAllOf(granted, spec.AllOfScopes)
		anySatisfied, missingAny := evaluateAnyOf(granted, spec.AnyOfScopes)
		enabled := len(missingAll) == 0 && (len(spec.AnyOfScopes) == 0 || anySatisfied)
		missing := append([]string(nil), missingAll...)
		if len(spec.AnyOfScopes) > 0 && !anySatisfied {
			missing = append(missing, missingAny...)
		}
		slices.Sort(missing)

		capabilities = append(capabilities, CapabilityStatus{
			ID:            spec.ID,
			Title:         spec.Title,
			Description:   spec.Description,
			CodeSupported: true,
			Enabled:       enabled,
			Requirements: CapabilityRequirement{
				AllOfScopes:                append([]string(nil), spec.AllOfScopes...),
				AnyOfScopes:                append([]string(nil), spec.AnyOfScopes...),
				RequiredEventSubscriptions: append([]string(nil), spec.Events...),
			},
			MissingScopes: missing,
			Notes:         append([]string(nil), spec.Notes...),
		})
	}

	return CapabilityReport{
		Provider:       "feishu",
		AccountID:      account.AccountID,
		ProfileID:      string(account.ProfileID),
		ConnectionMode: account.ConnectionMode,
		ScopeSummary:   scopes.Summary,
		Capabilities:   capabilities,
	}
}

func missingAllOf(granted map[string]struct{}, required []string) []string {
	missing := make([]string, 0, len(required))
	for _, scope := range required {
		if _, ok := granted[strings.TrimSpace(scope)]; ok {
			continue
		}
		missing = append(missing, strings.TrimSpace(scope))
	}
	return missing
}

func evaluateAnyOf(granted map[string]struct{}, options []string) (bool, []string) {
	if len(options) == 0 {
		return true, nil
	}
	missing := make([]string, 0, len(options))
	for _, scope := range options {
		trimmed := strings.TrimSpace(scope)
		if _, ok := granted[trimmed]; ok {
			return true, nil
		}
		missing = append(missing, trimmed)
	}
	return false, missing
}
