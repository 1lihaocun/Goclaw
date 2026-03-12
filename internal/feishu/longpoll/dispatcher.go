package longpoll

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	larkevent "github.com/larksuite/oapi-sdk-go/v3/event"
	larkdispatcher "github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"

	"goclaw/internal/domain"
	"goclaw/internal/feishu"
)

var ErrMissingMessageIngestor = errors.New("feishu longpoll: missing message ingestor")

type eventDispatcher struct {
	profileID domain.ProfileID
	ingestor  MessageEventIngestor
	observer  EventObserver
	logger    *slog.Logger
	onEvent   func(time.Time)
}

func newEventDispatcher(
	profileID domain.ProfileID,
	ingestor MessageEventIngestor,
	observer EventObserver,
	logger *slog.Logger,
	onEvent func(time.Time),
) (*eventDispatcher, error) {
	if ingestor == nil {
		return nil, ErrMissingMessageIngestor
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &eventDispatcher{
		profileID: profileID,
		ingestor:  ingestor,
		observer:  observer,
		logger:    logger,
		onEvent:   onEvent,
	}, nil
}

func (d *eventDispatcher) build() *larkdispatcher.EventDispatcher {
	dispatcher := larkdispatcher.NewEventDispatcher("", "").OnP2MessageReceiveV1(d.handleMessageReceiveV1)
	dispatcher = dispatcher.
		OnP2MessageRecalledV1(d.handleMessageRecalledV1).
		OnP2MessageReactionCreatedV1(d.handleMessageReactionCreatedV1).
		OnP2MessageReactionDeletedV1(d.handleMessageReactionDeletedV1).
		OnP2ChatMemberBotAddedV1(d.handleChatMemberBotAddedV1).
		OnP2ChatMemberBotDeletedV1(d.handleChatMemberBotDeletedV1).
		OnP2ChatMemberUserAddedV1(d.handleChatMemberUserAddedV1).
		OnP2ChatMemberUserDeletedV1(d.handleChatMemberUserDeletedV1).
		OnP2ChatMemberUserWithdrawnV1(d.handleChatMemberUserWithdrawnV1)
	return dispatcher
}

func (d *eventDispatcher) handleMessageReceiveV1(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
	messageEvent, err := sdkMessageEvent(event)
	if err != nil {
		d.logger.Error("feishu longpoll: decode message event failed", "error", err)
		return err
	}

	d.logger.Info(
		"feishu longpoll: received message event",
		"profile_id", d.profileID,
		"chat_id", messageEvent.Message.ChatID,
		"chat_type", messageEvent.Message.ChatType,
		"message_id", messageEvent.Message.MessageID,
		"sender_open_id", messageEvent.Sender.SenderID.OpenID,
	)

	d.markEvent()
	return d.ingestor.IngestFeishuMessageEvent(ctx, d.profileID, messageEvent)
}

func (d *eventDispatcher) handleMessageRecalledV1(ctx context.Context, event *larkim.P2MessageRecalledV1) error {
	rawEvent, err := sdkMessageRecalledEvent(event)
	if err != nil {
		d.logger.Error("feishu longpoll: decode recall event failed", "error", err)
		return err
	}
	return d.observe(ctx, rawEvent)
}

func (d *eventDispatcher) handleMessageReactionCreatedV1(
	ctx context.Context,
	event *larkim.P2MessageReactionCreatedV1,
) error {
	rawEvent, err := sdkMessageReactionCreatedEvent(event)
	if err != nil {
		d.logger.Error("feishu longpoll: decode reaction created event failed", "error", err)
		return err
	}
	return d.observe(ctx, rawEvent)
}

func (d *eventDispatcher) handleMessageReactionDeletedV1(
	ctx context.Context,
	event *larkim.P2MessageReactionDeletedV1,
) error {
	rawEvent, err := sdkMessageReactionDeletedEvent(event)
	if err != nil {
		d.logger.Error("feishu longpoll: decode reaction deleted event failed", "error", err)
		return err
	}
	return d.observe(ctx, rawEvent)
}

func (d *eventDispatcher) handleChatMemberBotAddedV1(ctx context.Context, event *larkim.P2ChatMemberBotAddedV1) error {
	rawEvent, err := sdkChatMemberBotAddedEvent(event)
	if err != nil {
		d.logger.Error("feishu longpoll: decode bot added event failed", "error", err)
		return err
	}
	return d.observe(ctx, rawEvent)
}

func (d *eventDispatcher) handleChatMemberBotDeletedV1(ctx context.Context, event *larkim.P2ChatMemberBotDeletedV1) error {
	rawEvent, err := sdkChatMemberBotDeletedEvent(event)
	if err != nil {
		d.logger.Error("feishu longpoll: decode bot deleted event failed", "error", err)
		return err
	}
	return d.observe(ctx, rawEvent)
}

func (d *eventDispatcher) handleChatMemberUserAddedV1(ctx context.Context, event *larkim.P2ChatMemberUserAddedV1) error {
	rawEvent, err := sdkChatMemberUserAddedEvent(event)
	if err != nil {
		d.logger.Error("feishu longpoll: decode user added event failed", "error", err)
		return err
	}
	return d.observe(ctx, rawEvent)
}

func (d *eventDispatcher) handleChatMemberUserDeletedV1(ctx context.Context, event *larkim.P2ChatMemberUserDeletedV1) error {
	rawEvent, err := sdkChatMemberUserDeletedEvent(event)
	if err != nil {
		d.logger.Error("feishu longpoll: decode user deleted event failed", "error", err)
		return err
	}
	return d.observe(ctx, rawEvent)
}

func (d *eventDispatcher) handleChatMemberUserWithdrawnV1(
	ctx context.Context,
	event *larkim.P2ChatMemberUserWithdrawnV1,
) error {
	rawEvent, err := sdkChatMemberUserWithdrawnEvent(event)
	if err != nil {
		d.logger.Error("feishu longpoll: decode user withdrawn event failed", "error", err)
		return err
	}
	return d.observe(ctx, rawEvent)
}

func (d *eventDispatcher) observe(ctx context.Context, event feishu.Event) error {
	d.logger.Info(
		"feishu longpoll: received channel event",
		"profile_id", d.profileID,
		"event_id", event.EventID,
		"event_type", event.EventType,
		"chat_id", event.ChatID,
		"message_id", event.MessageID,
	)
	d.markEvent()
	if d.observer == nil {
		return nil
	}
	return d.observer.ObserveFeishuEvent(ctx, d.profileID, event)
}

func (d *eventDispatcher) markEvent() {
	if d.onEvent != nil {
		d.onEvent(time.Now().UTC())
	}
}

func sdkMessageEvent(event *larkim.P2MessageReceiveV1) (feishu.MessageEvent, error) {
	if event == nil || event.Event == nil || event.Event.Message == nil || event.Event.Sender == nil {
		return feishu.MessageEvent{}, fmt.Errorf("feishu longpoll: incomplete message event")
	}

	return feishu.MessageEvent{
		Sender: feishu.Sender{
			SenderID: feishu.SenderID{
				OpenID:  sdkUserIDOpenID(event.Event.Sender.SenderId),
				UserID:  sdkUserIDUserID(event.Event.Sender.SenderId),
				UnionID: sdkUserIDUnionID(event.Event.Sender.SenderId),
			},
			SenderType: sdkString(event.Event.Sender.SenderType),
			TenantKey:  sdkString(event.Event.Sender.TenantKey),
		},
		Message: feishu.Message{
			MessageID:   sdkString(event.Event.Message.MessageId),
			RootID:      sdkString(event.Event.Message.RootId),
			ParentID:    sdkString(event.Event.Message.ParentId),
			ThreadID:    sdkString(event.Event.Message.ThreadId),
			ChatID:      sdkString(event.Event.Message.ChatId),
			ChatType:    feishu.ChatType(strings.TrimSpace(sdkString(event.Event.Message.ChatType))),
			MessageType: feishu.MessageType(strings.TrimSpace(sdkString(event.Event.Message.MessageType))),
			Content:     sdkString(event.Event.Message.Content),
			CreateTime:  sdkString(event.Event.Message.CreateTime),
			Mentions:    sdkMentions(event.Event.Message.Mentions),
		},
	}, nil
}

func sdkMessageRecalledEvent(event *larkim.P2MessageRecalledV1) (feishu.Event, error) {
	if event == nil || event.Event == nil {
		return feishu.Event{}, fmt.Errorf("feishu longpoll: incomplete recall event")
	}

	payload, err := marshalPayload(event.Event)
	if err != nil {
		return feishu.Event{}, err
	}
	return feishu.Event{
		EventID:    sdkEventID(event.EventV2Base),
		EventType:  "im.message.recalled_v1",
		ChatID:     sdkString(event.Event.ChatId),
		MessageID:  sdkString(event.Event.MessageId),
		OccurredAt: firstNonZeroTime(parseTimeString(sdkString(event.Event.RecallTime)), sdkEventTime(event.EventV2Base)),
		Payload:    payload,
	}, nil
}

func sdkMessageReactionCreatedEvent(event *larkim.P2MessageReactionCreatedV1) (feishu.Event, error) {
	if event == nil || event.Event == nil {
		return feishu.Event{}, fmt.Errorf("feishu longpoll: incomplete reaction created event")
	}

	payload, err := marshalPayload(event.Event)
	if err != nil {
		return feishu.Event{}, err
	}
	return feishu.Event{
		EventID:    sdkEventID(event.EventV2Base),
		EventType:  "im.message.reaction.created_v1",
		MessageID:  sdkString(event.Event.MessageId),
		Actor:      sdkSenderID(event.Event.UserId),
		ActorType:  sdkString(event.Event.OperatorType),
		ActorAppID: sdkString(event.Event.AppId),
		OccurredAt: firstNonZeroTime(parseTimeString(sdkString(event.Event.ActionTime)), sdkEventTime(event.EventV2Base)),
		Payload:    payload,
	}, nil
}

func sdkMessageReactionDeletedEvent(event *larkim.P2MessageReactionDeletedV1) (feishu.Event, error) {
	if event == nil || event.Event == nil {
		return feishu.Event{}, fmt.Errorf("feishu longpoll: incomplete reaction deleted event")
	}

	payload, err := marshalPayload(event.Event)
	if err != nil {
		return feishu.Event{}, err
	}
	return feishu.Event{
		EventID:    sdkEventID(event.EventV2Base),
		EventType:  "im.message.reaction.deleted_v1",
		MessageID:  sdkString(event.Event.MessageId),
		Actor:      sdkSenderID(event.Event.UserId),
		ActorType:  sdkString(event.Event.OperatorType),
		ActorAppID: sdkString(event.Event.AppId),
		OccurredAt: firstNonZeroTime(parseTimeString(sdkString(event.Event.ActionTime)), sdkEventTime(event.EventV2Base)),
		Payload:    payload,
	}, nil
}

func sdkChatMemberBotAddedEvent(event *larkim.P2ChatMemberBotAddedV1) (feishu.Event, error) {
	if event == nil || event.Event == nil {
		return feishu.Event{}, fmt.Errorf("feishu longpoll: incomplete bot added event")
	}

	payload, err := marshalPayload(event.Event)
	if err != nil {
		return feishu.Event{}, err
	}
	return feishu.Event{
		EventID:    sdkEventID(event.EventV2Base),
		EventType:  "im.chat.member.bot.added_v1",
		ChatID:     sdkString(event.Event.ChatId),
		ChatName:   sdkString(event.Event.Name),
		Actor:      sdkSenderID(event.Event.OperatorId),
		ActorType:  "user",
		OccurredAt: sdkEventTime(event.EventV2Base),
		Payload:    payload,
	}, nil
}

func sdkChatMemberBotDeletedEvent(event *larkim.P2ChatMemberBotDeletedV1) (feishu.Event, error) {
	if event == nil || event.Event == nil {
		return feishu.Event{}, fmt.Errorf("feishu longpoll: incomplete bot deleted event")
	}

	payload, err := marshalPayload(event.Event)
	if err != nil {
		return feishu.Event{}, err
	}
	return feishu.Event{
		EventID:    sdkEventID(event.EventV2Base),
		EventType:  "im.chat.member.bot.deleted_v1",
		ChatID:     sdkString(event.Event.ChatId),
		ChatName:   sdkString(event.Event.Name),
		Actor:      sdkSenderID(event.Event.OperatorId),
		ActorType:  "user",
		OccurredAt: sdkEventTime(event.EventV2Base),
		Payload:    payload,
	}, nil
}

func sdkChatMemberUserAddedEvent(event *larkim.P2ChatMemberUserAddedV1) (feishu.Event, error) {
	if event == nil || event.Event == nil {
		return feishu.Event{}, fmt.Errorf("feishu longpoll: incomplete user added event")
	}

	payload, err := marshalPayload(event.Event)
	if err != nil {
		return feishu.Event{}, err
	}
	return feishu.Event{
		EventID:    sdkEventID(event.EventV2Base),
		EventType:  "im.chat.member.user.added_v1",
		ChatID:     sdkString(event.Event.ChatId),
		ChatName:   sdkString(event.Event.Name),
		Actor:      sdkSenderID(event.Event.OperatorId),
		ActorType:  "user",
		OccurredAt: sdkEventTime(event.EventV2Base),
		Payload:    payload,
	}, nil
}

func sdkChatMemberUserDeletedEvent(event *larkim.P2ChatMemberUserDeletedV1) (feishu.Event, error) {
	if event == nil || event.Event == nil {
		return feishu.Event{}, fmt.Errorf("feishu longpoll: incomplete user deleted event")
	}

	payload, err := marshalPayload(event.Event)
	if err != nil {
		return feishu.Event{}, err
	}
	return feishu.Event{
		EventID:    sdkEventID(event.EventV2Base),
		EventType:  "im.chat.member.user.deleted_v1",
		ChatID:     sdkString(event.Event.ChatId),
		ChatName:   sdkString(event.Event.Name),
		Actor:      sdkSenderID(event.Event.OperatorId),
		ActorType:  "user",
		OccurredAt: sdkEventTime(event.EventV2Base),
		Payload:    payload,
	}, nil
}

func sdkChatMemberUserWithdrawnEvent(event *larkim.P2ChatMemberUserWithdrawnV1) (feishu.Event, error) {
	if event == nil || event.Event == nil {
		return feishu.Event{}, fmt.Errorf("feishu longpoll: incomplete user withdrawn event")
	}

	payload, err := marshalPayload(event.Event)
	if err != nil {
		return feishu.Event{}, err
	}
	return feishu.Event{
		EventID:    sdkEventID(event.EventV2Base),
		EventType:  "im.chat.member.user.withdrawn_v1",
		ChatID:     sdkString(event.Event.ChatId),
		ChatName:   sdkString(event.Event.Name),
		Actor:      sdkSenderID(event.Event.OperatorId),
		ActorType:  "user",
		OccurredAt: sdkEventTime(event.EventV2Base),
		Payload:    payload,
	}, nil
}

func marshalPayload(value any) (json.RawMessage, error) {
	if value == nil {
		return nil, nil
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("feishu longpoll: marshal payload: %w", err)
	}
	return payload, nil
}

func sdkEventID(base *larkevent.EventV2Base) string {
	if base == nil || base.Header == nil {
		return ""
	}
	return strings.TrimSpace(base.Header.EventID)
}

func sdkEventTime(base *larkevent.EventV2Base) time.Time {
	if base == nil || base.Header == nil {
		return time.Time{}
	}
	return parseTimeString(base.Header.CreateTime)
}

func sdkMentions(mentions []*larkim.MentionEvent) []feishu.Mention {
	if len(mentions) == 0 {
		return nil
	}

	out := make([]feishu.Mention, 0, len(mentions))
	for _, mention := range mentions {
		if mention == nil {
			continue
		}
		out = append(out, feishu.Mention{
			Key: sdkString(mention.Key),
			ID: feishu.SenderID{
				OpenID:  sdkUserIDOpenID(mention.Id),
				UserID:  sdkUserIDUserID(mention.Id),
				UnionID: sdkUserIDUnionID(mention.Id),
			},
			Name:      sdkString(mention.Name),
			TenantKey: sdkString(mention.TenantKey),
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func sdkString(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func sdkSenderID(value *larkim.UserId) feishu.SenderID {
	return feishu.SenderID{
		OpenID:  sdkUserIDOpenID(value),
		UserID:  sdkUserIDUserID(value),
		UnionID: sdkUserIDUnionID(value),
	}
}

func sdkUserIDOpenID(value *larkim.UserId) string {
	if value == nil {
		return ""
	}
	return sdkString(value.OpenId)
}

func sdkUserIDUserID(value *larkim.UserId) string {
	if value == nil {
		return ""
	}
	return sdkString(value.UserId)
}

func sdkUserIDUnionID(value *larkim.UserId) string {
	if value == nil {
		return ""
	}
	return sdkString(value.UnionId)
}

func parseTimeString(value string) time.Time {
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

func firstNonZeroTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value.UTC()
		}
	}
	return time.Time{}
}
