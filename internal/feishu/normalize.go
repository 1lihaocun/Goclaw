package feishu

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"goclaw/internal/domain"
)

var (
	ErrMissingProfileID = errors.New("feishu: missing profile id")
	ErrMissingChatID    = errors.New("feishu: missing chat_id")
	ErrMissingMessageID = errors.New("feishu: missing message_id")
	ErrMissingOpenID    = errors.New("feishu: missing sender open_id")
)

func NormalizeMessageEvent(profileID domain.ProfileID, event MessageEvent) (InboundMessage, error) {
	if strings.TrimSpace(string(profileID)) == "" {
		return InboundMessage{}, ErrMissingProfileID
	}

	chatID := strings.TrimSpace(event.Message.ChatID)
	if chatID == "" {
		return InboundMessage{}, ErrMissingChatID
	}

	messageID := strings.TrimSpace(event.Message.MessageID)
	if messageID == "" {
		return InboundMessage{}, ErrMissingMessageID
	}

	openID := strings.TrimSpace(event.Sender.SenderID.OpenID)
	if openID == "" {
		return InboundMessage{}, ErrMissingOpenID
	}

	return InboundMessage{
		ProfileID:         profileID,
		RoomID:            domain.RoomID(chatID),
		RoomKind:          roomKindFromChatType(event.Message.ChatType),
		PersonID:          domain.PersonID(openID),
		ProviderMessageID: messageID,
		ContentText:       parseMessageContent(event.Message.Content, event.Message.MessageType),
		ReceivedAt:        parseCreateTime(event.Message.CreateTime),
		ChatType:          event.Message.ChatType,
		ContentType:       event.Message.MessageType,
		RootID:            strings.TrimSpace(event.Message.RootID),
		ParentID:          strings.TrimSpace(event.Message.ParentID),
		ThreadID:          strings.TrimSpace(event.Message.ThreadID),
	}, nil
}

func roomKindFromChatType(chatType ChatType) domain.RoomKind {
	switch chatType {
	case ChatTypeP2P, ChatTypePrivate:
		return domain.RoomKindDirect
	default:
		return domain.RoomKindGroup
	}
}

func parseMessageContent(content string, messageType MessageType) string {
	switch messageType {
	case MessageTypePost:
		return parsePostText(content)
	case MessageTypeText:
		var parsed struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(content), &parsed); err == nil && parsed.Text != "" {
			return parsed.Text
		}
		return content
	case MessageTypeShareChat:
		var parsed struct {
			Body        string `json:"body"`
			Summary     string `json:"summary"`
			ShareChatID string `json:"share_chat_id"`
		}
		if err := json.Unmarshal([]byte(content), &parsed); err != nil {
			return "[Forwarded message]"
		}
		if body := strings.TrimSpace(parsed.Body); body != "" {
			return body
		}
		if summary := strings.TrimSpace(parsed.Summary); summary != "" {
			return summary
		}
		if shareChatID := strings.TrimSpace(parsed.ShareChatID); shareChatID != "" {
			return "[Forwarded message: " + shareChatID + "]"
		}
		return "[Forwarded message]"
	case MessageTypeMergeForward:
		return "[Merged and Forwarded Message - loading...]"
	default:
		return content
	}
}

func parseCreateTime(value string) time.Time {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}
	}

	if unix, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
		switch {
		case len(trimmed) >= 16:
			return time.UnixMicro(unix).UTC()
		case len(trimmed) >= 13:
			return time.UnixMilli(unix).UTC()
		case len(trimmed) >= 10:
			return time.Unix(unix, 0).UTC()
		}
	}

	if parsed, err := time.Parse(time.RFC3339Nano, trimmed); err == nil {
		return parsed.UTC()
	}
	if parsed, err := time.Parse(time.RFC3339, trimmed); err == nil {
		return parsed.UTC()
	}
	return time.Time{}
}
