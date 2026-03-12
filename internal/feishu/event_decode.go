package feishu

import (
	"encoding/json"
	"fmt"
	"strings"
)

func decodeEvent(header *EventHeader, payload json.RawMessage) (Event, error) {
	eventType := ""
	eventID := ""
	occurredAt := parseCreateTime("")
	if header != nil {
		eventType = strings.TrimSpace(header.EventType)
		eventID = strings.TrimSpace(header.EventID)
		occurredAt = parseCreateTime(header.CreateTime)
	}

	switch eventType {
	case "im.message.recalled_v1":
		var recalled recalledEventPayload
		if err := json.Unmarshal(payload, &recalled); err != nil {
			return Event{}, fmt.Errorf("decode feishu recall event: %w", err)
		}
		if recallAt := parseCreateTime(recalled.RecallTime); !recallAt.IsZero() {
			occurredAt = recallAt
		}
		return Event{
			EventID:    eventID,
			EventType:  eventType,
			ChatID:     strings.TrimSpace(recalled.ChatID),
			MessageID:  strings.TrimSpace(recalled.MessageID),
			OccurredAt: occurredAt,
			Payload:    cloneRawJSON(payload),
		}, nil
	case "im.message.reaction.created_v1", "im.message.reaction.deleted_v1":
		var reaction reactionEventPayload
		if err := json.Unmarshal(payload, &reaction); err != nil {
			return Event{}, fmt.Errorf("decode feishu reaction event: %w", err)
		}
		if actionAt := parseCreateTime(reaction.ActionTime); !actionAt.IsZero() {
			occurredAt = actionAt
		}
		return Event{
			EventID:    eventID,
			EventType:  eventType,
			MessageID:  strings.TrimSpace(reaction.MessageID),
			Actor:      reaction.UserID,
			ActorType:  strings.TrimSpace(reaction.OperatorType),
			ActorAppID: strings.TrimSpace(reaction.AppID),
			OccurredAt: occurredAt,
			Payload:    cloneRawJSON(payload),
		}, nil
	case "im.chat.member.bot.added_v1", "im.chat.member.bot.deleted_v1",
		"im.chat.member.user.added_v1", "im.chat.member.user.deleted_v1", "im.chat.member.user.withdrawn_v1":
		var member chatMemberEventPayload
		if err := json.Unmarshal(payload, &member); err != nil {
			return Event{}, fmt.Errorf("decode feishu member event: %w", err)
		}
		return Event{
			EventID:    eventID,
			EventType:  eventType,
			ChatID:     strings.TrimSpace(member.ChatID),
			ChatName:   strings.TrimSpace(member.Name),
			Actor:      member.OperatorID,
			ActorType:  "user",
			OccurredAt: occurredAt,
			Payload:    cloneRawJSON(payload),
		}, nil
	default:
		return Event{}, fmt.Errorf("feishu: unsupported event type %q", eventType)
	}
}

func cloneRawJSON(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return nil
	}
	out := make([]byte, len(value))
	copy(out, value)
	return out
}
