package feishu

import (
	"encoding/json"
	"time"
)

type ChatType string

const (
	ChatTypeP2P     ChatType = "p2p"
	ChatTypeGroup   ChatType = "group"
	ChatTypePrivate ChatType = "private"
)

type MessageType string

const (
	MessageTypeText         MessageType = "text"
	MessageTypePost         MessageType = "post"
	MessageTypeShareChat    MessageType = "share_chat"
	MessageTypeMergeForward MessageType = "merge_forward"
)

type SenderID struct {
	OpenID  string `json:"open_id,omitempty"`
	UserID  string `json:"user_id,omitempty"`
	UnionID string `json:"union_id,omitempty"`
}

type Sender struct {
	SenderID   SenderID `json:"sender_id"`
	SenderType string   `json:"sender_type,omitempty"`
	TenantKey  string   `json:"tenant_key,omitempty"`
}

type Mention struct {
	Key       string   `json:"key"`
	ID        SenderID `json:"id"`
	Name      string   `json:"name"`
	TenantKey string   `json:"tenant_key,omitempty"`
}

type Message struct {
	MessageID   string      `json:"message_id"`
	RootID      string      `json:"root_id,omitempty"`
	ParentID    string      `json:"parent_id,omitempty"`
	ThreadID    string      `json:"thread_id,omitempty"`
	ChatID      string      `json:"chat_id"`
	ChatType    ChatType    `json:"chat_type"`
	MessageType MessageType `json:"message_type"`
	Content     string      `json:"content"`
	CreateTime  string      `json:"create_time,omitempty"`
	Mentions    []Mention   `json:"mentions,omitempty"`
}

type MessageEvent struct {
	Sender  Sender  `json:"sender"`
	Message Message `json:"message"`
}

type Event struct {
	EventID    string          `json:"event_id,omitempty"`
	EventType  string          `json:"event_type,omitempty"`
	ChatID     string          `json:"chat_id,omitempty"`
	ChatName   string          `json:"chat_name,omitempty"`
	MessageID  string          `json:"message_id,omitempty"`
	Actor      SenderID        `json:"actor,omitempty"`
	ActorType  string          `json:"actor_type,omitempty"`
	ActorAppID string          `json:"actor_app_id,omitempty"`
	OccurredAt time.Time       `json:"-"`
	Payload    json.RawMessage `json:"payload,omitempty"`
}

type recalledEventPayload struct {
	MessageID  string `json:"message_id"`
	ChatID     string `json:"chat_id"`
	RecallTime string `json:"recall_time,omitempty"`
	RecallType string `json:"recall_type,omitempty"`
}

type reactionEventPayload struct {
	MessageID    string   `json:"message_id"`
	ReactionType emojiRef `json:"reaction_type"`
	OperatorType string   `json:"operator_type,omitempty"`
	UserID       SenderID `json:"user_id"`
	AppID        string   `json:"app_id,omitempty"`
	ActionTime   string   `json:"action_time,omitempty"`
}

type chatMemberEventPayload struct {
	ChatID            string            `json:"chat_id"`
	OperatorID        SenderID          `json:"operator_id"`
	External          bool              `json:"external,omitempty"`
	OperatorTenantKey string            `json:"operator_tenant_key,omitempty"`
	Users             []chatMemberUser  `json:"users,omitempty"`
	Name              string            `json:"name,omitempty"`
	I18nNames         map[string]string `json:"i18n_names,omitempty"`
}

type chatMemberUser struct {
	Name      string   `json:"name,omitempty"`
	TenantKey string   `json:"tenant_key,omitempty"`
	UserID    SenderID `json:"user_id"`
}

type emojiRef struct {
	EmojiType string `json:"emoji_type,omitempty"`
}
