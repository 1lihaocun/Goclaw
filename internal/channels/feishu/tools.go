package feishuchannel

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"goclaw/internal/agenttools"
	channelcore "goclaw/internal/channels"
	"goclaw/internal/config"
	"goclaw/internal/domain"
	rawfeishu "goclaw/internal/feishu"
)

var (
	feishuNoArgsSchema  = json.RawMessage(`{"type":"object","properties":{}}`)
	feishuChatGetSchema = json.RawMessage(`{
		"type":"object",
		"properties":{
			"chat_id":{"type":"string"}
		}
	}`)
	feishuChatMembersSchema = json.RawMessage(`{
		"type":"object",
		"properties":{
			"chat_id":{"type":"string"},
			"page_size":{"type":"integer"},
			"page_token":{"type":"string"}
		}
	}`)
	feishuReactionListSchema = json.RawMessage(`{
		"type":"object",
		"properties":{
			"message_id":{"type":"string"},
			"reaction_type":{"type":"string"},
			"page_size":{"type":"integer"},
			"page_token":{"type":"string"}
		}
	}`)
	feishuPinsListSchema = json.RawMessage(`{
		"type":"object",
		"properties":{
			"chat_id":{"type":"string"},
			"page_size":{"type":"integer"},
			"page_token":{"type":"string"}
		}
	}`)
	feishuMessageSendSchema = json.RawMessage(`{
		"type":"object",
		"properties":{
			"receive_id":{"type":"string"},
			"receive_id_type":{"type":"string"},
			"chat_id":{"type":"string"},
			"msg_type":{"type":"string"},
			"content_json":{"type":"string"},
			"uuid":{"type":"string"}
		},
		"required":["msg_type","content_json"]
	}`)
	feishuMessageReplySchema = json.RawMessage(`{
		"type":"object",
		"properties":{
			"message_id":{"type":"string"},
			"msg_type":{"type":"string"},
			"content_json":{"type":"string"},
			"reply_in_thread":{"type":"boolean"},
			"uuid":{"type":"string"}
		},
		"required":["msg_type","content_json"]
	}`)
	feishuMessageForwardSchema = json.RawMessage(`{
		"type":"object",
		"properties":{
			"message_id":{"type":"string"},
			"receive_id":{"type":"string"},
			"receive_id_type":{"type":"string"},
			"chat_id":{"type":"string"}
		}
	}`)
	feishuMessageMergeForwardSchema = json.RawMessage(`{
		"type":"object",
		"properties":{
			"message_ids":{"type":"array","items":{"type":"string"}},
			"receive_id":{"type":"string"},
			"receive_id_type":{"type":"string"},
			"chat_id":{"type":"string"}
		},
		"required":["message_ids"]
	}`)
	feishuThreadForwardSchema = json.RawMessage(`{
		"type":"object",
		"properties":{
			"thread_id":{"type":"string"},
			"receive_id":{"type":"string"},
			"receive_id_type":{"type":"string"},
			"chat_id":{"type":"string"}
		},
		"required":["thread_id"]
	}`)
	feishuMessageFollowUpSchema = json.RawMessage(`{
		"type":"object",
		"properties":{
			"message_id":{"type":"string"},
			"follow_ups_json":{"type":"string"}
		},
		"required":["follow_ups_json"]
	}`)
	feishuMessageUrgentSchema = json.RawMessage(`{
		"type":"object",
		"properties":{
			"message_id":{"type":"string"},
			"user_id_type":{"type":"string"},
			"user_ids":{"type":"array","items":{"type":"string"}}
		},
		"required":["user_ids"]
	}`)
	feishuPostSendSchema = json.RawMessage(`{
		"type":"object",
		"properties":{
			"chat_id":{"type":"string"},
			"content_json":{"type":"string"}
		},
		"required":["content_json"]
	}`)
	feishuDocReadSchema = json.RawMessage(`{
		"type":"object",
		"properties":{
			"document_id":{"type":"string"},
			"document_type":{"type":"string"},
			"wiki_token":{"type":"string"}
		}
	}`)
	feishuMessageUpdateSchema = json.RawMessage(`{
		"type":"object",
		"properties":{
			"message_id":{"type":"string"},
			"text":{"type":"string"}
		},
		"required":["message_id","text"]
	}`)
	feishuMessageRecallSchema = json.RawMessage(`{
		"type":"object",
		"properties":{
			"message_id":{"type":"string"}
		},
		"required":["message_id"]
	}`)
	feishuReactionAddSchema = json.RawMessage(`{
		"type":"object",
		"properties":{
			"message_id":{"type":"string"},
			"emoji_type":{"type":"string"}
		},
		"required":["emoji_type"]
	}`)
	feishuReactionDeleteSchema = json.RawMessage(`{
		"type":"object",
		"properties":{
			"message_id":{"type":"string"},
			"reaction_id":{"type":"string"}
		},
		"required":["reaction_id"]
	}`)
	feishuPinSchema = json.RawMessage(`{
		"type":"object",
		"properties":{
			"message_id":{"type":"string"}
		}
	}`)
)

type toolProvider struct {
	cfg       config.FeishuConfig
	newClient func(Account) (*rawfeishu.Client, error)
}

func (c *Channel) AgentToolProviders(_ channelcore.ToolBuildParams) ([]agenttools.Provider, error) {
	return []agenttools.Provider{
		toolProvider{cfg: c.config},
	}, nil
}

func (p toolProvider) VisibleTools(
	session agenttools.SessionContext,
	policy domain.ToolPermissionPolicy,
) []agenttools.Definition {
	if session.Provider != domain.ProviderFeishu {
		return nil
	}
	account, ok := ResolveAccountByProfile(p.cfg, session.ProfileID)
	if !ok || !account.Enabled || !account.Configured {
		return nil
	}

	definitions := []agenttools.Definition{
		p.scopeListTool(account),
		p.capabilitiesStatusTool(account),
		p.chatGetTool(account),
		p.chatMembersTool(account),
	}
	if account.Actions.Reactions {
		definitions = append(definitions,
			p.reactionListTool(account),
			p.reactionAddTool(account),
			p.reactionDeleteTool(account),
		)
	}
	if account.Actions.Pins {
		definitions = append(definitions,
			p.pinsListTool(account),
			p.pinAddTool(account),
			p.pinDeleteTool(account),
		)
	}
	if account.Actions.MessageSend || account.Actions.PostMessages {
		definitions = append(definitions,
			p.messageSendTool(account),
			p.postSendTool(account),
			p.messageReplyTool(account),
			p.messageForwardTool(account),
			p.messageMergeForwardTool(account),
			p.threadForwardTool(account),
			p.messageFollowUpTool(account),
			p.messageUrgentAppTool(account),
			p.messageUrgentPhoneTool(account),
			p.messageUrgentSMSTool(account),
		)
	}
	if account.Actions.DocsRead {
		definitions = append(definitions, p.docReadTool(account))
	}
	if account.Actions.MessageUpdate {
		definitions = append(definitions, p.messageUpdateTool(account))
	}
	if account.Actions.MessageRecall {
		definitions = append(definitions, p.messageRecallTool(account))
	}
	visible := make([]agenttools.Definition, 0, len(definitions))
	for _, definition := range definitions {
		if !agenttools.AllowsChannelTool(policy, session, definition) {
			continue
		}
		visible = append(visible, p.bindDefinition(session, account, definition))
	}
	return visible
}

func (p toolProvider) CapabilitySummary(
	ctx context.Context,
	session agenttools.SessionContext,
	_ domain.ToolPermissionPolicy,
) (agenttools.CapabilitySummary, bool, error) {
	if session.Provider != domain.ProviderFeishu {
		return agenttools.CapabilitySummary{}, false, nil
	}
	account, ok := ResolveAccountByProfile(p.cfg, session.ProfileID)
	if !ok || !account.Enabled || !account.Configured {
		return agenttools.CapabilitySummary{}, true, nil
	}
	client, err := p.newClientForAccount(account)
	if err != nil {
		return agenttools.CapabilitySummary{}, true, err
	}
	scopes, err := client.ListAppScopes(ctx)
	if err != nil {
		return agenttools.CapabilitySummary{}, true, err
	}
	return agenttools.CapabilitySummary{ScopeSummary: scopes.Summary}, true, nil
}

func (p toolProvider) scopeListTool(account Account) agenttools.Definition {
	return agenttools.Definition{
		Name:         "feishu_app_scopes",
		Description:  "List currently granted and pending Feishu app scopes for the active account.",
		InputSchema:  append(json.RawMessage(nil), feishuNoArgsSchema...),
		Source:       agenttools.SourceChannel,
		Provider:     domain.ProviderFeishu,
		SafetyClass:  agenttools.SafetyIntrospection,
		CapabilityID: "feishu.im.app_scopes.read",
		Execute: func(ctx context.Context, execCtx agenttools.ExecutionContext, _ json.RawMessage) (any, error) {
			client, err := p.newClientForAccount(account)
			if err != nil {
				return nil, providerError("feishu_app_scopes", "resolve feishu client", err)
			}
			scopes, err := client.ListAppScopes(ctx)
			if err != nil {
				return nil, normalizeFeishuToolError("feishu_app_scopes", "", err)
			}
			return scopes, nil
		},
	}
}

func (p toolProvider) capabilitiesStatusTool(account Account) agenttools.Definition {
	return agenttools.Definition{
		Name:         "feishu_capabilities_status",
		Description:  "Map current Feishu app scopes onto GoClaw's supported Feishu channel capabilities.",
		InputSchema:  append(json.RawMessage(nil), feishuNoArgsSchema...),
		Source:       agenttools.SourceChannel,
		Provider:     domain.ProviderFeishu,
		SafetyClass:  agenttools.SafetyIntrospection,
		CapabilityID: "feishu.im.capabilities.read",
		Execute: func(ctx context.Context, execCtx agenttools.ExecutionContext, _ json.RawMessage) (any, error) {
			client, err := p.newClientForAccount(account)
			if err != nil {
				return nil, providerError("feishu_capabilities_status", "resolve feishu client", err)
			}
			if strings.TrimSpace(execCtx.Account.AccountID) == "" {
				return nil, &agenttools.ExecutionError{
					Kind:       agenttools.ErrorProvider,
					Provider:   domain.ProviderFeishu,
					Tool:       "feishu_capabilities_status",
					Message:    fmt.Sprintf("feishu channel: profile %q not bound to an account", execCtx.Session.ProfileID),
					Capability: "feishu.im.capabilities.read",
				}
			}
			scopes, err := client.ListAppScopes(ctx)
			if err != nil {
				return nil, normalizeFeishuToolError("feishu_capabilities_status", "feishu.im.capabilities.read", err)
			}
			return BuildCapabilityReport(Account{
				AccountID:      execCtx.Account.AccountID,
				Name:           execCtx.Account.Name,
				ProfileID:      execCtx.Account.ProfileID,
				Enabled:        execCtx.Account.Enabled,
				Configured:     execCtx.Account.Configured,
				ConnectionMode: execCtx.Account.ConnectionMode,
			}, scopes), nil
		},
	}
}

func (p toolProvider) chatGetTool(account Account) agenttools.Definition {
	return agenttools.Definition{
		Name:         "feishu_chat_get",
		Description:  "Read Feishu chat metadata for the current room or a specified chat_id.",
		InputSchema:  append(json.RawMessage(nil), feishuChatGetSchema...),
		Source:       agenttools.SourceChannel,
		Provider:     domain.ProviderFeishu,
		SafetyClass:  agenttools.SafetyIntrospection,
		CapabilityID: "feishu.im.chat.read",
		Execute: func(ctx context.Context, execCtx agenttools.ExecutionContext, raw json.RawMessage) (any, error) {
			var args struct {
				ChatID string `json:"chat_id"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, invalidInputError("feishu_chat_get", "parse tool args", err)
			}
			chatID := firstNonEmpty(strings.TrimSpace(args.ChatID), strings.TrimSpace(execCtx.ProviderMetadata.ProviderRoomID), string(execCtx.Session.RoomID))
			if chatID == "" {
				return nil, &agenttools.ExecutionError{
					Kind:       agenttools.ErrorInvalidInput,
					Provider:   domain.ProviderFeishu,
					Tool:       "feishu_chat_get",
					Message:    "missing chat_id and current room has no provider_room_id",
					Capability: "feishu.im.chat.read",
				}
			}
			client, err := p.newClientForAccount(account)
			if err != nil {
				return nil, providerError("feishu_chat_get", "resolve feishu client", err)
			}
			chat, err := client.GetChat(ctx, chatID)
			if err != nil {
				return nil, normalizeFeishuToolError("feishu_chat_get", "feishu.im.chat.read", err)
			}
			return chat, nil
		},
	}
}

func (p toolProvider) chatMembersTool(account Account) agenttools.Definition {
	return agenttools.Definition{
		Name:         "feishu_chat_members",
		Description:  "List Feishu chat members for the current room or a specified chat_id.",
		InputSchema:  append(json.RawMessage(nil), feishuChatMembersSchema...),
		Source:       agenttools.SourceChannel,
		Provider:     domain.ProviderFeishu,
		SafetyClass:  agenttools.SafetyIntrospection,
		CapabilityID: "feishu.im.chat.members.read",
		Execute: func(ctx context.Context, execCtx agenttools.ExecutionContext, raw json.RawMessage) (any, error) {
			var args struct {
				ChatID    string `json:"chat_id"`
				PageSize  int    `json:"page_size"`
				PageToken string `json:"page_token"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, invalidInputError("feishu_chat_members", "parse tool args", err)
			}
			chatID := firstNonEmpty(strings.TrimSpace(args.ChatID), strings.TrimSpace(execCtx.ProviderMetadata.ProviderRoomID), string(execCtx.Session.RoomID))
			if chatID == "" {
				return nil, &agenttools.ExecutionError{
					Kind:       agenttools.ErrorInvalidInput,
					Provider:   domain.ProviderFeishu,
					Tool:       "feishu_chat_members",
					Message:    "missing chat_id and current room has no provider_room_id",
					Capability: "feishu.im.chat.members.read",
				}
			}
			client, err := p.newClientForAccount(account)
			if err != nil {
				return nil, providerError("feishu_chat_members", "resolve feishu client", err)
			}
			page, err := client.ListChatMembers(ctx, chatID, args.PageSize, strings.TrimSpace(args.PageToken))
			if err != nil {
				return nil, normalizeFeishuToolError("feishu_chat_members", "feishu.im.chat.members.read", err)
			}
			return page, nil
		},
	}
}

func (p toolProvider) reactionListTool(account Account) agenttools.Definition {
	return agenttools.Definition{
		Name:         "feishu_reactions_list",
		Description:  "List reactions for the current inbound Feishu message or a specified message_id.",
		InputSchema:  append(json.RawMessage(nil), feishuReactionListSchema...),
		Source:       agenttools.SourceChannel,
		Provider:     domain.ProviderFeishu,
		SafetyClass:  agenttools.SafetyReadOnly,
		CapabilityID: "feishu.im.message.reactions.read",
		Execute: func(ctx context.Context, execCtx agenttools.ExecutionContext, raw json.RawMessage) (any, error) {
			var args struct {
				MessageID    string `json:"message_id"`
				ReactionType string `json:"reaction_type"`
				PageSize     int    `json:"page_size"`
				PageToken    string `json:"page_token"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, invalidInputError("feishu_reactions_list", "parse tool args", err)
			}
			messageID, err := resolveMessageID(
				strings.TrimSpace(args.MessageID),
				strings.TrimSpace(execCtx.ProviderMetadata.CurrentMessageProviderID),
			)
			if err != nil {
				return nil, &agenttools.ExecutionError{
					Kind:       agenttools.ErrorInvalidInput,
					Provider:   domain.ProviderFeishu,
					Tool:       "feishu_reactions_list",
					Message:    err.Error(),
					Capability: "feishu.im.message.reactions.read",
				}
			}
			client, err := p.newClientForAccount(account)
			if err != nil {
				return nil, providerError("feishu_reactions_list", "resolve feishu client", err)
			}
			page, err := client.ListMessageReactions(ctx, messageID, strings.TrimSpace(args.ReactionType), args.PageSize, strings.TrimSpace(args.PageToken))
			if err != nil {
				return nil, normalizeFeishuToolError("feishu_reactions_list", "feishu.im.message.reactions.read", err)
			}
			return page, nil
		},
	}
}

func (p toolProvider) pinsListTool(account Account) agenttools.Definition {
	return agenttools.Definition{
		Name:         "feishu_pins_list",
		Description:  "List pinned messages for the current Feishu room or a specified chat_id.",
		InputSchema:  append(json.RawMessage(nil), feishuPinsListSchema...),
		Source:       agenttools.SourceChannel,
		Provider:     domain.ProviderFeishu,
		SafetyClass:  agenttools.SafetyReadOnly,
		CapabilityID: "feishu.im.message.pins.read",
		Execute: func(ctx context.Context, execCtx agenttools.ExecutionContext, raw json.RawMessage) (any, error) {
			var args struct {
				ChatID    string `json:"chat_id"`
				PageSize  int    `json:"page_size"`
				PageToken string `json:"page_token"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, invalidInputError("feishu_pins_list", "parse tool args", err)
			}
			chatID := firstNonEmpty(strings.TrimSpace(args.ChatID), strings.TrimSpace(execCtx.ProviderMetadata.ProviderRoomID), string(execCtx.Session.RoomID))
			if chatID == "" {
				return nil, &agenttools.ExecutionError{
					Kind:       agenttools.ErrorInvalidInput,
					Provider:   domain.ProviderFeishu,
					Tool:       "feishu_pins_list",
					Message:    "missing chat_id and current room has no provider_room_id",
					Capability: "feishu.im.message.pins.read",
				}
			}
			client, err := p.newClientForAccount(account)
			if err != nil {
				return nil, providerError("feishu_pins_list", "resolve feishu client", err)
			}
			page, err := client.ListPins(ctx, chatID, args.PageSize, strings.TrimSpace(args.PageToken))
			if err != nil {
				return nil, normalizeFeishuToolError("feishu_pins_list", "feishu.im.message.pins.read", err)
			}
			return page, nil
		},
	}
}

func (p toolProvider) postSendTool(account Account) agenttools.Definition {
	return agenttools.Definition{
		Name:         "feishu_message_post_send",
		Description:  "Send a Feishu rich-text post message to the current room or a specified chat_id using raw post content_json.",
		InputSchema:  append(json.RawMessage(nil), feishuPostSendSchema...),
		Source:       agenttools.SourceChannel,
		Provider:     domain.ProviderFeishu,
		SafetyClass:  agenttools.SafetyMutating,
		CapabilityID: "feishu.im.message.post.send",
		Execute: func(ctx context.Context, execCtx agenttools.ExecutionContext, raw json.RawMessage) (any, error) {
			var args struct {
				ChatID      string `json:"chat_id"`
				ContentJSON string `json:"content_json"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, invalidInputError("feishu_message_post_send", "parse tool args", err)
			}
			chatID := firstNonEmpty(strings.TrimSpace(args.ChatID), strings.TrimSpace(execCtx.ProviderMetadata.ProviderRoomID), string(execCtx.Session.RoomID))
			if chatID == "" {
				return nil, &agenttools.ExecutionError{
					Kind:       agenttools.ErrorInvalidInput,
					Provider:   domain.ProviderFeishu,
					Tool:       "feishu_message_post_send",
					Message:    "missing chat_id and current room has no provider_room_id",
					Capability: "feishu.im.message.post.send",
				}
			}
			contentJSON := strings.TrimSpace(args.ContentJSON)
			if contentJSON == "" {
				return nil, &agenttools.ExecutionError{
					Kind:       agenttools.ErrorInvalidInput,
					Provider:   domain.ProviderFeishu,
					Tool:       "feishu_message_post_send",
					Message:    "missing content_json",
					Capability: "feishu.im.message.post.send",
				}
			}
			if !json.Valid([]byte(contentJSON)) {
				return nil, &agenttools.ExecutionError{
					Kind:       agenttools.ErrorInvalidInput,
					Provider:   domain.ProviderFeishu,
					Tool:       "feishu_message_post_send",
					Message:    "content_json must be valid json",
					Capability: "feishu.im.message.post.send",
				}
			}
			client, err := p.newClientForAccount(account)
			if err != nil {
				return nil, providerError("feishu_message_post_send", "resolve feishu client", err)
			}
			result, err := client.SendPost(ctx, chatID, contentJSON)
			if err != nil {
				return nil, normalizeFeishuToolError("feishu_message_post_send", "feishu.im.message.post.send", err)
			}
			return result, nil
		},
	}
}

func (p toolProvider) messageSendTool(account Account) agenttools.Definition {
	return agenttools.Definition{
		Name:         "feishu_message_send",
		Description:  "Send a Feishu bot message to a room or user with an explicitly supported msg_type such as text, post, interactive, image, file, audio, media, sticker, share_chat, or share_user.",
		InputSchema:  append(json.RawMessage(nil), feishuMessageSendSchema...),
		Source:       agenttools.SourceChannel,
		Provider:     domain.ProviderFeishu,
		SafetyClass:  agenttools.SafetyMutating,
		CapabilityID: "feishu.im.message.send",
		Execute: func(ctx context.Context, execCtx agenttools.ExecutionContext, raw json.RawMessage) (any, error) {
			var args struct {
				ReceiveID     string `json:"receive_id"`
				ReceiveIDType string `json:"receive_id_type"`
				ChatID        string `json:"chat_id"`
				MsgType       string `json:"msg_type"`
				ContentJSON   string `json:"content_json"`
				UUID          string `json:"uuid"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, invalidInputError("feishu_message_send", "parse tool args", err)
			}
			receiveID, receiveIDType, err := resolveReceiveTarget(args.ReceiveID, args.ChatID, args.ReceiveIDType, execCtx.ProviderMetadata.ProviderRoomID, false)
			if err != nil {
				return nil, toolInvalidInput("feishu_message_send", "feishu.im.message.send", err)
			}
			msgType, err := normalizeSendMessageType(args.MsgType)
			if err != nil {
				return nil, toolInvalidInput("feishu_message_send", "feishu.im.message.send", err)
			}
			contentJSON := strings.TrimSpace(args.ContentJSON)
			if contentJSON == "" {
				return nil, toolInvalidInput("feishu_message_send", "feishu.im.message.send", fmt.Errorf("missing content_json"))
			}
			client, err := p.newClientForAccount(account)
			if err != nil {
				return nil, providerError("feishu_message_send", "resolve feishu client", err)
			}
			result, err := client.SendMessage(ctx, rawfeishu.SendMessageRequest{
				ReceiveIDType: receiveIDType,
				ReceiveID:     receiveID,
				MsgType:       msgType,
				ContentJSON:   contentJSON,
				UUID:          strings.TrimSpace(args.UUID),
			})
			if err != nil {
				return nil, normalizeFeishuToolError("feishu_message_send", "feishu.im.message.send", err)
			}
			return result, nil
		},
	}
}

func (p toolProvider) messageReplyTool(account Account) agenttools.Definition {
	return agenttools.Definition{
		Name:         "feishu_message_reply",
		Description:  "Reply to a Feishu message with an explicitly supported msg_type such as text, post, interactive, image, file, audio, media, sticker, share_card, or share_user.",
		InputSchema:  append(json.RawMessage(nil), feishuMessageReplySchema...),
		Source:       agenttools.SourceChannel,
		Provider:     domain.ProviderFeishu,
		SafetyClass:  agenttools.SafetyMutating,
		CapabilityID: "feishu.im.message.reply",
		Execute: func(ctx context.Context, execCtx agenttools.ExecutionContext, raw json.RawMessage) (any, error) {
			var args struct {
				MessageID     string `json:"message_id"`
				MsgType       string `json:"msg_type"`
				ContentJSON   string `json:"content_json"`
				ReplyInThread bool   `json:"reply_in_thread"`
				UUID          string `json:"uuid"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, invalidInputError("feishu_message_reply", "parse tool args", err)
			}
			messageID, err := resolveMessageID(strings.TrimSpace(args.MessageID), strings.TrimSpace(execCtx.ProviderMetadata.CurrentMessageProviderID))
			if err != nil {
				return nil, toolInvalidInput("feishu_message_reply", "feishu.im.message.reply", err)
			}
			msgType, err := normalizeReplyMessageType(args.MsgType)
			if err != nil {
				return nil, toolInvalidInput("feishu_message_reply", "feishu.im.message.reply", err)
			}
			contentJSON := strings.TrimSpace(args.ContentJSON)
			if contentJSON == "" {
				return nil, toolInvalidInput("feishu_message_reply", "feishu.im.message.reply", fmt.Errorf("missing content_json"))
			}
			client, err := p.newClientForAccount(account)
			if err != nil {
				return nil, providerError("feishu_message_reply", "resolve feishu client", err)
			}
			result, err := client.ReplyMessage(ctx, rawfeishu.ReplyMessageRequest{
				MessageID:     messageID,
				MsgType:       msgType,
				ContentJSON:   contentJSON,
				ReplyInThread: args.ReplyInThread,
				UUID:          strings.TrimSpace(args.UUID),
			})
			if err != nil {
				return nil, normalizeFeishuToolError("feishu_message_reply", "feishu.im.message.reply", err)
			}
			return result, nil
		},
	}
}

func (p toolProvider) messageForwardTool(account Account) agenttools.Definition {
	return agenttools.Definition{
		Name:         "feishu_message_forward",
		Description:  "Forward a Feishu message to another room, user, or thread target.",
		InputSchema:  append(json.RawMessage(nil), feishuMessageForwardSchema...),
		Source:       agenttools.SourceChannel,
		Provider:     domain.ProviderFeishu,
		SafetyClass:  agenttools.SafetyMutating,
		CapabilityID: "feishu.im.message.forward",
		Execute: func(ctx context.Context, execCtx agenttools.ExecutionContext, raw json.RawMessage) (any, error) {
			var args struct {
				MessageID     string `json:"message_id"`
				ReceiveID     string `json:"receive_id"`
				ReceiveIDType string `json:"receive_id_type"`
				ChatID        string `json:"chat_id"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, invalidInputError("feishu_message_forward", "parse tool args", err)
			}
			messageID, err := resolveMessageID(strings.TrimSpace(args.MessageID), strings.TrimSpace(execCtx.ProviderMetadata.CurrentMessageProviderID))
			if err != nil {
				return nil, toolInvalidInput("feishu_message_forward", "feishu.im.message.forward", err)
			}
			receiveID, receiveIDType, err := resolveReceiveTarget(args.ReceiveID, args.ChatID, args.ReceiveIDType, execCtx.ProviderMetadata.ProviderRoomID, true)
			if err != nil {
				return nil, toolInvalidInput("feishu_message_forward", "feishu.im.message.forward", err)
			}
			client, err := p.newClientForAccount(account)
			if err != nil {
				return nil, providerError("feishu_message_forward", "resolve feishu client", err)
			}
			result, err := client.ForwardMessage(ctx, rawfeishu.ForwardMessageRequest{MessageID: messageID, ReceiveID: receiveID, ReceiveIDType: receiveIDType})
			if err != nil {
				return nil, normalizeFeishuToolError("feishu_message_forward", "feishu.im.message.forward", err)
			}
			return result, nil
		},
	}
}

func (p toolProvider) messageMergeForwardTool(account Account) agenttools.Definition {
	return agenttools.Definition{
		Name:         "feishu_message_merge_forward",
		Description:  "Merge-forward multiple Feishu messages into another room, user, or thread target.",
		InputSchema:  append(json.RawMessage(nil), feishuMessageMergeForwardSchema...),
		Source:       agenttools.SourceChannel,
		Provider:     domain.ProviderFeishu,
		SafetyClass:  agenttools.SafetyMutating,
		CapabilityID: "feishu.im.message.merge_forward",
		Execute: func(ctx context.Context, execCtx agenttools.ExecutionContext, raw json.RawMessage) (any, error) {
			var args struct {
				MessageIDs    []string `json:"message_ids"`
				ReceiveID     string   `json:"receive_id"`
				ReceiveIDType string   `json:"receive_id_type"`
				ChatID        string   `json:"chat_id"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, invalidInputError("feishu_message_merge_forward", "parse tool args", err)
			}
			receiveID, receiveIDType, err := resolveReceiveTarget(args.ReceiveID, args.ChatID, args.ReceiveIDType, execCtx.ProviderMetadata.ProviderRoomID, true)
			if err != nil {
				return nil, toolInvalidInput("feishu_message_merge_forward", "feishu.im.message.merge_forward", err)
			}
			client, err := p.newClientForAccount(account)
			if err != nil {
				return nil, providerError("feishu_message_merge_forward", "resolve feishu client", err)
			}
			result, err := client.MergeForwardMessages(ctx, rawfeishu.MergeForwardMessagesRequest{ReceiveID: receiveID, ReceiveIDType: receiveIDType, MessageIDs: args.MessageIDs})
			if err != nil {
				return nil, normalizeFeishuToolError("feishu_message_merge_forward", "feishu.im.message.merge_forward", err)
			}
			return result, nil
		},
	}
}

func (p toolProvider) threadForwardTool(account Account) agenttools.Definition {
	return agenttools.Definition{
		Name:         "feishu_thread_forward",
		Description:  "Forward a Feishu thread to another room, user, or thread target.",
		InputSchema:  append(json.RawMessage(nil), feishuThreadForwardSchema...),
		Source:       agenttools.SourceChannel,
		Provider:     domain.ProviderFeishu,
		SafetyClass:  agenttools.SafetyMutating,
		CapabilityID: "feishu.im.thread.forward",
		Execute: func(ctx context.Context, execCtx agenttools.ExecutionContext, raw json.RawMessage) (any, error) {
			var args struct {
				ThreadID      string `json:"thread_id"`
				ReceiveID     string `json:"receive_id"`
				ReceiveIDType string `json:"receive_id_type"`
				ChatID        string `json:"chat_id"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, invalidInputError("feishu_thread_forward", "parse tool args", err)
			}
			threadID := strings.TrimSpace(args.ThreadID)
			if threadID == "" {
				return nil, toolInvalidInput("feishu_thread_forward", "feishu.im.thread.forward", fmt.Errorf("missing thread_id"))
			}
			receiveID, receiveIDType, err := resolveReceiveTarget(args.ReceiveID, args.ChatID, args.ReceiveIDType, execCtx.ProviderMetadata.ProviderRoomID, true)
			if err != nil {
				return nil, toolInvalidInput("feishu_thread_forward", "feishu.im.thread.forward", err)
			}
			client, err := p.newClientForAccount(account)
			if err != nil {
				return nil, providerError("feishu_thread_forward", "resolve feishu client", err)
			}
			result, err := client.ForwardThread(ctx, rawfeishu.ThreadForwardRequest{ThreadID: threadID, ReceiveID: receiveID, ReceiveIDType: receiveIDType})
			if err != nil {
				return nil, normalizeFeishuToolError("feishu_thread_forward", "feishu.im.thread.forward", err)
			}
			return result, nil
		},
	}
}

func (p toolProvider) messageFollowUpTool(account Account) agenttools.Definition {
	return agenttools.Definition{
		Name:         "feishu_message_follow_up_push",
		Description:  "Push follow-up text blocks onto a previously sent Feishu message.",
		InputSchema:  append(json.RawMessage(nil), feishuMessageFollowUpSchema...),
		Source:       agenttools.SourceChannel,
		Provider:     domain.ProviderFeishu,
		SafetyClass:  agenttools.SafetyMutating,
		CapabilityID: "feishu.im.message.follow_up.push",
		Execute: func(ctx context.Context, execCtx agenttools.ExecutionContext, raw json.RawMessage) (any, error) {
			var args struct {
				MessageID     string `json:"message_id"`
				FollowUpsJSON string `json:"follow_ups_json"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, invalidInputError("feishu_message_follow_up_push", "parse tool args", err)
			}
			messageID, err := resolveMessageID(strings.TrimSpace(args.MessageID), strings.TrimSpace(execCtx.ProviderMetadata.CurrentMessageProviderID))
			if err != nil {
				return nil, toolInvalidInput("feishu_message_follow_up_push", "feishu.im.message.follow_up.push", err)
			}
			client, err := p.newClientForAccount(account)
			if err != nil {
				return nil, providerError("feishu_message_follow_up_push", "resolve feishu client", err)
			}
			if err := client.PushFollowUp(ctx, rawfeishu.PushFollowUpRequest{MessageID: messageID, FollowUpsJSON: strings.TrimSpace(args.FollowUpsJSON)}); err != nil {
				return nil, normalizeFeishuToolError("feishu_message_follow_up_push", "feishu.im.message.follow_up.push", err)
			}
			return map[string]any{"message_id": messageID, "pushed": true}, nil
		},
	}
}

func (p toolProvider) messageUrgentAppTool(account Account) agenttools.Definition {
	return p.messageUrgentTool(account, "feishu_message_urgent_app", "Send an in-app urgent reminder for a previously sent Feishu message.", "feishu.im.message.urgent.app", func(ctx context.Context, client *rawfeishu.Client, req rawfeishu.UrgentMessageRequest) (rawfeishu.UrgentResult, error) {
		return client.UrgentApp(ctx, req)
	})
}

func (p toolProvider) messageUrgentPhoneTool(account Account) agenttools.Definition {
	return p.messageUrgentTool(account, "feishu_message_urgent_phone", "Send an in-app plus phone urgent reminder for a previously sent Feishu message.", "feishu.im.message.urgent.phone", func(ctx context.Context, client *rawfeishu.Client, req rawfeishu.UrgentMessageRequest) (rawfeishu.UrgentResult, error) {
		return client.UrgentPhone(ctx, req)
	})
}

func (p toolProvider) messageUrgentSMSTool(account Account) agenttools.Definition {
	return p.messageUrgentTool(account, "feishu_message_urgent_sms", "Send an in-app plus SMS urgent reminder for a previously sent Feishu message.", "feishu.im.message.urgent.sms", func(ctx context.Context, client *rawfeishu.Client, req rawfeishu.UrgentMessageRequest) (rawfeishu.UrgentResult, error) {
		return client.UrgentSms(ctx, req)
	})
}

func (p toolProvider) messageUrgentTool(account Account, toolName, description, capabilityID string, run func(context.Context, *rawfeishu.Client, rawfeishu.UrgentMessageRequest) (rawfeishu.UrgentResult, error)) agenttools.Definition {
	return agenttools.Definition{
		Name:         toolName,
		Description:  description,
		InputSchema:  append(json.RawMessage(nil), feishuMessageUrgentSchema...),
		Source:       agenttools.SourceChannel,
		Provider:     domain.ProviderFeishu,
		SafetyClass:  agenttools.SafetyMutating,
		CapabilityID: capabilityID,
		Execute: func(ctx context.Context, execCtx agenttools.ExecutionContext, raw json.RawMessage) (any, error) {
			var args struct {
				MessageID  string   `json:"message_id"`
				UserIDType string   `json:"user_id_type"`
				UserIDs    []string `json:"user_ids"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, invalidInputError(toolName, "parse tool args", err)
			}
			messageID, err := resolveMessageID(strings.TrimSpace(args.MessageID), strings.TrimSpace(execCtx.ProviderMetadata.CurrentMessageProviderID))
			if err != nil {
				return nil, toolInvalidInput(toolName, capabilityID, err)
			}
			userIDType, err := normalizeUrgentUserIDType(args.UserIDType)
			if err != nil {
				return nil, toolInvalidInput(toolName, capabilityID, err)
			}
			client, err := p.newClientForAccount(account)
			if err != nil {
				return nil, providerError(toolName, "resolve feishu client", err)
			}
			result, err := run(ctx, client, rawfeishu.UrgentMessageRequest{MessageID: messageID, UserIDType: userIDType, UserIDs: args.UserIDs})
			if err != nil {
				return nil, normalizeFeishuToolError(toolName, capabilityID, err)
			}
			return result, nil
		},
	}
}

func (p toolProvider) docReadTool(account Account) agenttools.Definition {
	return agenttools.Definition{
		Name:         "feishu_doc_read",
		Description:  "Read a Feishu document as raw text by document_id, or resolve a wiki_token and then read the backing doc/docx.",
		InputSchema:  append(json.RawMessage(nil), feishuDocReadSchema...),
		Source:       agenttools.SourceChannel,
		Provider:     domain.ProviderFeishu,
		SafetyClass:  agenttools.SafetyReadOnly,
		CapabilityID: "feishu.docs.document.read",
		Execute: func(ctx context.Context, execCtx agenttools.ExecutionContext, raw json.RawMessage) (any, error) {
			var args struct {
				DocumentID   string `json:"document_id"`
				DocumentType string `json:"document_type"`
				WikiToken    string `json:"wiki_token"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, invalidInputError("feishu_doc_read", "parse tool args", err)
			}
			client, err := p.newClientForAccount(account)
			if err != nil {
				return nil, providerError("feishu_doc_read", "resolve feishu client", err)
			}
			documentID := strings.TrimSpace(args.DocumentID)
			documentType := strings.TrimSpace(strings.ToLower(args.DocumentType))
			var wikiNode *rawfeishu.WikiNodeInfo
			if wikiToken := strings.TrimSpace(args.WikiToken); wikiToken != "" {
				node, err := client.GetWikiNode(ctx, wikiToken)
				if err != nil {
					return nil, normalizeFeishuToolError("feishu_doc_read", "feishu.docs.document.read", err)
				}
				wikiNode = &node
				documentID = strings.TrimSpace(node.ObjToken)
				documentType = strings.TrimSpace(strings.ToLower(node.ObjType))
			}
			if documentID == "" {
				return nil, &agenttools.ExecutionError{
					Kind:       agenttools.ErrorInvalidInput,
					Provider:   domain.ProviderFeishu,
					Tool:       "feishu_doc_read",
					Message:    "missing document_id or wiki_token",
					Capability: "feishu.docs.document.read",
				}
			}
			if documentType == "" {
				documentType = inferDocumentType(documentID)
			}
			var content rawfeishu.DocumentRawContent
			switch documentType {
			case "docx":
				content, err = client.GetDocumentRawContent(ctx, documentID)
			case "doc":
				content, err = client.GetLegacyDocumentRawContent(ctx, documentID)
			default:
				return nil, &agenttools.ExecutionError{
					Kind:       agenttools.ErrorInvalidInput,
					Provider:   domain.ProviderFeishu,
					Tool:       "feishu_doc_read",
					Message:    fmt.Sprintf("unsupported feishu document_type %q", documentType),
					Capability: "feishu.docs.document.read",
				}
			}
			if err != nil {
				return nil, normalizeFeishuToolError("feishu_doc_read", "feishu.docs.document.read", err)
			}
			result := map[string]any{
				"document_id":   content.DocumentID,
				"document_type": content.DocumentType,
				"content":       content.Content,
			}
			if wikiNode != nil {
				result["wiki"] = wikiNode
			}
			return result, nil
		},
	}
}

func (p toolProvider) messageUpdateTool(account Account) agenttools.Definition {
	return agenttools.Definition{
		Name:         "feishu_message_update",
		Description:  "Update a Feishu bot message with new plain-text content.",
		InputSchema:  append(json.RawMessage(nil), feishuMessageUpdateSchema...),
		Source:       agenttools.SourceChannel,
		Provider:     domain.ProviderFeishu,
		SafetyClass:  agenttools.SafetyMutating,
		CapabilityID: "feishu.im.message.update",
		Execute: func(ctx context.Context, execCtx agenttools.ExecutionContext, raw json.RawMessage) (any, error) {
			var args struct {
				MessageID string `json:"message_id"`
				Text      string `json:"text"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, invalidInputError("feishu_message_update", "parse tool args", err)
			}
			messageID := strings.TrimSpace(args.MessageID)
			if messageID == "" {
				return nil, &agenttools.ExecutionError{
					Kind:       agenttools.ErrorInvalidInput,
					Provider:   domain.ProviderFeishu,
					Tool:       "feishu_message_update",
					Message:    "missing message_id",
					Capability: "feishu.im.message.update",
				}
			}
			contentJSON, err := feishuTextContentJSON(args.Text)
			if err != nil {
				return nil, invalidInputError("feishu_message_update", "marshal message content", err)
			}
			client, err := p.newClientForAccount(account)
			if err != nil {
				return nil, providerError("feishu_message_update", "resolve feishu client", err)
			}
			if err := client.UpdateMessageContent(ctx, messageID, contentJSON); err != nil {
				return nil, normalizeFeishuToolError("feishu_message_update", "feishu.im.message.update", err)
			}
			return map[string]any{
				"message_id": messageID,
				"updated":    true,
			}, nil
		},
	}
}

func (p toolProvider) messageRecallTool(account Account) agenttools.Definition {
	return agenttools.Definition{
		Name:         "feishu_message_recall",
		Description:  "Recall a previously sent Feishu bot message.",
		InputSchema:  append(json.RawMessage(nil), feishuMessageRecallSchema...),
		Source:       agenttools.SourceChannel,
		Provider:     domain.ProviderFeishu,
		SafetyClass:  agenttools.SafetyMutating,
		CapabilityID: "feishu.im.message.recall",
		Execute: func(ctx context.Context, execCtx agenttools.ExecutionContext, raw json.RawMessage) (any, error) {
			var args struct {
				MessageID string `json:"message_id"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, invalidInputError("feishu_message_recall", "parse tool args", err)
			}
			messageID := strings.TrimSpace(args.MessageID)
			if messageID == "" {
				return nil, &agenttools.ExecutionError{
					Kind:       agenttools.ErrorInvalidInput,
					Provider:   domain.ProviderFeishu,
					Tool:       "feishu_message_recall",
					Message:    "missing message_id",
					Capability: "feishu.im.message.recall",
				}
			}
			client, err := p.newClientForAccount(account)
			if err != nil {
				return nil, providerError("feishu_message_recall", "resolve feishu client", err)
			}
			if err := client.RecallMessage(ctx, messageID); err != nil {
				return nil, normalizeFeishuToolError("feishu_message_recall", "feishu.im.message.recall", err)
			}
			return map[string]any{
				"message_id": messageID,
				"recalled":   true,
			}, nil
		},
	}
}

func (p toolProvider) reactionAddTool(account Account) agenttools.Definition {
	return agenttools.Definition{
		Name:         "feishu_reaction_add",
		Description:  "Add a reaction to a Feishu message. Defaults to the current inbound message when message_id is omitted.",
		InputSchema:  append(json.RawMessage(nil), feishuReactionAddSchema...),
		Source:       agenttools.SourceChannel,
		Provider:     domain.ProviderFeishu,
		SafetyClass:  agenttools.SafetyMutating,
		CapabilityID: "feishu.im.message.reactions.write",
		Execute: func(ctx context.Context, execCtx agenttools.ExecutionContext, raw json.RawMessage) (any, error) {
			var args struct {
				MessageID string `json:"message_id"`
				EmojiType string `json:"emoji_type"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, invalidInputError("feishu_reaction_add", "parse tool args", err)
			}
			messageID, err := resolveMessageID(
				strings.TrimSpace(args.MessageID),
				strings.TrimSpace(execCtx.ProviderMetadata.CurrentMessageProviderID),
			)
			if err != nil {
				return nil, &agenttools.ExecutionError{
					Kind:       agenttools.ErrorInvalidInput,
					Provider:   domain.ProviderFeishu,
					Tool:       "feishu_reaction_add",
					Message:    err.Error(),
					Capability: "feishu.im.message.reactions.write",
				}
			}
			emojiType := strings.TrimSpace(args.EmojiType)
			if emojiType == "" {
				return nil, &agenttools.ExecutionError{
					Kind:       agenttools.ErrorInvalidInput,
					Provider:   domain.ProviderFeishu,
					Tool:       "feishu_reaction_add",
					Message:    "missing emoji_type",
					Capability: "feishu.im.message.reactions.write",
				}
			}
			client, err := p.newClientForAccount(account)
			if err != nil {
				return nil, providerError("feishu_reaction_add", "resolve feishu client", err)
			}
			reaction, err := client.AddMessageReaction(ctx, messageID, emojiType)
			if err != nil {
				return nil, normalizeFeishuToolError("feishu_reaction_add", "feishu.im.message.reactions.write", err)
			}
			return reaction, nil
		},
	}
}

func (p toolProvider) reactionDeleteTool(account Account) agenttools.Definition {
	return agenttools.Definition{
		Name:         "feishu_reaction_delete",
		Description:  "Delete a reaction from a Feishu message. Defaults to the current inbound message when message_id is omitted.",
		InputSchema:  append(json.RawMessage(nil), feishuReactionDeleteSchema...),
		Source:       agenttools.SourceChannel,
		Provider:     domain.ProviderFeishu,
		SafetyClass:  agenttools.SafetyMutating,
		CapabilityID: "feishu.im.message.reactions.write",
		Execute: func(ctx context.Context, execCtx agenttools.ExecutionContext, raw json.RawMessage) (any, error) {
			var args struct {
				MessageID  string `json:"message_id"`
				ReactionID string `json:"reaction_id"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, invalidInputError("feishu_reaction_delete", "parse tool args", err)
			}
			messageID, err := resolveMessageID(
				strings.TrimSpace(args.MessageID),
				strings.TrimSpace(execCtx.ProviderMetadata.CurrentMessageProviderID),
			)
			if err != nil {
				return nil, &agenttools.ExecutionError{
					Kind:       agenttools.ErrorInvalidInput,
					Provider:   domain.ProviderFeishu,
					Tool:       "feishu_reaction_delete",
					Message:    err.Error(),
					Capability: "feishu.im.message.reactions.write",
				}
			}
			reactionID := strings.TrimSpace(args.ReactionID)
			if reactionID == "" {
				return nil, &agenttools.ExecutionError{
					Kind:       agenttools.ErrorInvalidInput,
					Provider:   domain.ProviderFeishu,
					Tool:       "feishu_reaction_delete",
					Message:    "missing reaction_id",
					Capability: "feishu.im.message.reactions.write",
				}
			}
			client, err := p.newClientForAccount(account)
			if err != nil {
				return nil, providerError("feishu_reaction_delete", "resolve feishu client", err)
			}
			reaction, err := client.RemoveMessageReaction(ctx, messageID, reactionID)
			if err != nil {
				return nil, normalizeFeishuToolError("feishu_reaction_delete", "feishu.im.message.reactions.write", err)
			}
			return reaction, nil
		},
	}
}

func (p toolProvider) pinAddTool(account Account) agenttools.Definition {
	return agenttools.Definition{
		Name:         "feishu_pin_add",
		Description:  "Pin a Feishu message. Defaults to the current inbound message when message_id is omitted.",
		InputSchema:  append(json.RawMessage(nil), feishuPinSchema...),
		Source:       agenttools.SourceChannel,
		Provider:     domain.ProviderFeishu,
		SafetyClass:  agenttools.SafetyMutating,
		CapabilityID: "feishu.im.message.pins.write",
		Execute: func(ctx context.Context, execCtx agenttools.ExecutionContext, raw json.RawMessage) (any, error) {
			var args struct {
				MessageID string `json:"message_id"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, invalidInputError("feishu_pin_add", "parse tool args", err)
			}
			messageID, err := resolveMessageID(
				strings.TrimSpace(args.MessageID),
				strings.TrimSpace(execCtx.ProviderMetadata.CurrentMessageProviderID),
			)
			if err != nil {
				return nil, &agenttools.ExecutionError{
					Kind:       agenttools.ErrorInvalidInput,
					Provider:   domain.ProviderFeishu,
					Tool:       "feishu_pin_add",
					Message:    err.Error(),
					Capability: "feishu.im.message.pins.write",
				}
			}
			client, err := p.newClientForAccount(account)
			if err != nil {
				return nil, providerError("feishu_pin_add", "resolve feishu client", err)
			}
			pin, err := client.PinMessage(ctx, messageID)
			if err != nil {
				return nil, normalizeFeishuToolError("feishu_pin_add", "feishu.im.message.pins.write", err)
			}
			return pin, nil
		},
	}
}

func (p toolProvider) pinDeleteTool(account Account) agenttools.Definition {
	return agenttools.Definition{
		Name:         "feishu_pin_delete",
		Description:  "Unpin a Feishu message. Defaults to the current inbound message when message_id is omitted.",
		InputSchema:  append(json.RawMessage(nil), feishuPinSchema...),
		Source:       agenttools.SourceChannel,
		Provider:     domain.ProviderFeishu,
		SafetyClass:  agenttools.SafetyMutating,
		CapabilityID: "feishu.im.message.pins.write",
		Execute: func(ctx context.Context, execCtx agenttools.ExecutionContext, raw json.RawMessage) (any, error) {
			var args struct {
				MessageID string `json:"message_id"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, invalidInputError("feishu_pin_delete", "parse tool args", err)
			}
			messageID, err := resolveMessageID(
				strings.TrimSpace(args.MessageID),
				strings.TrimSpace(execCtx.ProviderMetadata.CurrentMessageProviderID),
			)
			if err != nil {
				return nil, &agenttools.ExecutionError{
					Kind:       agenttools.ErrorInvalidInput,
					Provider:   domain.ProviderFeishu,
					Tool:       "feishu_pin_delete",
					Message:    err.Error(),
					Capability: "feishu.im.message.pins.write",
				}
			}
			client, err := p.newClientForAccount(account)
			if err != nil {
				return nil, providerError("feishu_pin_delete", "resolve feishu client", err)
			}
			if err := client.UnpinMessage(ctx, messageID); err != nil {
				return nil, normalizeFeishuToolError("feishu_pin_delete", "feishu.im.message.pins.write", err)
			}
			return map[string]any{
				"message_id": messageID,
				"unpinned":   true,
			}, nil
		},
	}
}

func (p toolProvider) bindDefinition(
	session agenttools.SessionContext,
	account Account,
	definition agenttools.Definition,
) agenttools.Definition {
	definition.ProfileBindingMode = agenttools.ProfileBindingCurrentSession
	definition.Account = agenttools.AccountMetadata{
		Provider:       domain.ProviderFeishu,
		AccountID:      strings.TrimSpace(account.AccountID),
		Name:           strings.TrimSpace(account.Name),
		ProfileID:      account.ProfileID,
		Enabled:        account.Enabled,
		Configured:     account.Configured,
		ConnectionMode: strings.TrimSpace(account.ConnectionMode),
	}
	definition.ProviderMetadata = agenttools.ProviderMetadata{
		RoomKind:                 session.RoomKind,
		ProviderRoomID:           strings.TrimSpace(session.ProviderRoomID),
		ProviderUserID:           strings.TrimSpace(session.ProviderUserID),
		CurrentMessageProviderID: strings.TrimSpace(session.CurrentMessageProviderID),
	}
	return definition
}

func (p toolProvider) newClientForAccount(account Account) (*rawfeishu.Client, error) {
	if p.newClient != nil {
		return p.newClient(account)
	}
	if strings.TrimSpace(string(account.ProfileID)) == "" {
		return nil, fmt.Errorf("feishu channel: account %q has no bound profile", account.AccountID)
	}
	if !account.Enabled {
		return nil, fmt.Errorf("feishu channel: profile %q is disabled", account.ProfileID)
	}
	if !account.Configured {
		return nil, fmt.Errorf("feishu channel: profile %q is not fully configured", account.ProfileID)
	}
	return rawfeishu.NewClient(rawfeishu.ClientConfig{
		BaseURL:   account.APIBaseURL,
		AppID:     account.AppID,
		AppSecret: account.AppSecret,
	}), nil
}

func normalizeFeishuToolError(toolName, capabilityID string, err error) error {
	if permissionErr, ok := rawfeishu.ExtractPermissionError(err); ok {
		details := map[string]any{
			"missing_scopes": append([]string(nil), permissionErr.MissingScopes...),
		}
		return &agenttools.ExecutionError{
			Kind:       agenttools.ErrorPermissionDenied,
			Provider:   domain.ProviderFeishu,
			Tool:       toolName,
			Message:    firstNonEmpty(permissionErr.Message, "feishu permission denied"),
			GrantURL:   permissionErr.GrantURL,
			Details:    details,
			Capability: capabilityID,
		}
	}
	return &agenttools.ExecutionError{
		Kind:       agenttools.ErrorProvider,
		Provider:   domain.ProviderFeishu,
		Tool:       toolName,
		Message:    err.Error(),
		Capability: capabilityID,
	}
}

func toolInvalidInput(toolName, capabilityID string, err error) error {
	return &agenttools.ExecutionError{
		Kind:       agenttools.ErrorInvalidInput,
		Provider:   domain.ProviderFeishu,
		Tool:       toolName,
		Message:    err.Error(),
		Capability: capabilityID,
	}
}

func resolveReceiveTarget(explicitReceiveID, explicitChatID, explicitReceiveIDType, fallbackRoomID string, allowThread bool) (string, string, error) {
	receiveID := firstNonEmpty(explicitReceiveID, explicitChatID)
	receiveIDType := strings.TrimSpace(explicitReceiveIDType)
	if receiveID == "" {
		fallbackRoomID = strings.TrimSpace(fallbackRoomID)
		if fallbackRoomID == "" {
			return "", "", fmt.Errorf("missing receive_id/chat_id and current room has no provider_room_id")
		}
		return fallbackRoomID, "chat_id", nil
	}
	if receiveIDType == "" && strings.TrimSpace(explicitChatID) != "" {
		receiveIDType = "chat_id"
	}
	receiveIDType, err := normalizeReceiveIDType(receiveIDType, allowThread)
	if err != nil {
		return "", "", err
	}
	return receiveID, receiveIDType, nil
}

func normalizeReceiveIDType(value string, allowThread bool) (string, error) {
	switch strings.TrimSpace(value) {
	case "", "chat_id":
		return "chat_id", nil
	case "open_id", "user_id", "union_id", "email":
		return strings.TrimSpace(value), nil
	case "thread_id":
		if allowThread {
			return "thread_id", nil
		}
	}
	return "", fmt.Errorf("unsupported receive_id_type %q", strings.TrimSpace(value))
}

func normalizeSendMessageType(value string) (string, error) {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "text", "post", "image", "file", "audio", "media", "sticker", "interactive", "share_chat", "share_user":
		return strings.TrimSpace(strings.ToLower(value)), nil
	default:
		return "", fmt.Errorf("unsupported feishu msg_type %q", strings.TrimSpace(value))
	}
}

func normalizeReplyMessageType(value string) (string, error) {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "text", "post", "image", "file", "audio", "media", "sticker", "interactive", "share_chat", "share_card", "share_user":
		return strings.TrimSpace(strings.ToLower(value)), nil
	default:
		return "", fmt.Errorf("unsupported feishu reply msg_type %q", strings.TrimSpace(value))
	}
}

func normalizeUrgentUserIDType(value string) (string, error) {
	switch strings.TrimSpace(value) {
	case "", "open_id":
		return "open_id", nil
	case "user_id", "union_id":
		return strings.TrimSpace(value), nil
	default:
		return "", fmt.Errorf("unsupported user_id_type %q", strings.TrimSpace(value))
	}
}

func providerError(toolName, operation string, err error) error {
	return &agenttools.ExecutionError{
		Kind:     agenttools.ErrorProvider,
		Provider: domain.ProviderFeishu,
		Tool:     toolName,
		Message:  strings.TrimSpace(operation + ": " + err.Error()),
	}
}

func invalidInputError(toolName, operation string, err error) error {
	return &agenttools.ExecutionError{
		Kind:     agenttools.ErrorInvalidInput,
		Provider: domain.ProviderFeishu,
		Tool:     toolName,
		Message:  strings.TrimSpace(operation + ": " + err.Error()),
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func feishuTextContentJSON(text string) (string, error) {
	payload := struct {
		Text string `json:"text"`
	}{
		Text: strings.TrimSpace(text),
	}
	if payload.Text == "" {
		return "", fmt.Errorf("missing text")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func resolveMessageID(explicitMessageID string, fallbackMessageID string) (string, error) {
	messageID := firstNonEmpty(
		normalizeFeishuMessageID(explicitMessageID),
		normalizeFeishuMessageID(fallbackMessageID),
	)
	if messageID == "" {
		return "", fmt.Errorf("missing message_id and current message has no provider_message_id")
	}
	return messageID, nil
}

func inferDocumentType(documentID string) string {
	trimmed := strings.ToLower(strings.TrimSpace(documentID))
	switch {
	case strings.HasPrefix(trimmed, "dox"):
		return "docx"
	case strings.HasPrefix(trimmed, "doc"):
		return "doc"
	default:
		return "docx"
	}
}

func normalizeFeishuMessageID(value string) string {
	trimmed := strings.TrimSpace(value)
	return strings.TrimPrefix(trimmed, "msg:feishu:")
}
