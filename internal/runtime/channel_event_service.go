package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	channelcore "goclaw/internal/channels"
	"goclaw/internal/domain"
	sqlitestore "goclaw/internal/store/sqlite"
)

const recalledMessageText = "[recalled]"

type ChannelEventService struct {
	Repos       sqlitestore.Repositories
	ChatService *ChatService
	Logger      *slog.Logger
	Now         func() time.Time
}

func NewChannelEventService(repos sqlitestore.Repositories, logger *slog.Logger) *ChannelEventService {
	if logger == nil {
		logger = slog.Default()
	}
	return &ChannelEventService{
		Repos:       repos,
		ChatService: NewChatService(repos),
		Logger:      logger,
		Now:         time.Now,
	}
}

func (s *ChannelEventService) ObserveChannelEvent(
	ctx context.Context,
	event channelcore.ChannelEvent,
) error {
	stored := domain.ChannelEvent{
		ID:                buildChannelEventID(event, s.Now),
		Provider:          event.Provider,
		ProfileID:         event.ProfileID,
		EventID:           fallbackEventID(event, s.Now),
		Type:              strings.TrimSpace(event.Type),
		RoomID:            event.RoomID,
		ProviderRoomID:    strings.TrimSpace(event.ProviderRoomID),
		PersonID:          event.PersonID,
		ProviderUserID:    strings.TrimSpace(event.ProviderUserID),
		ProviderMessageID: strings.TrimSpace(event.ProviderMessageID),
		PayloadJSON:       normalizePayloadJSON(event.PayloadJSON),
		OccurredAt:        effectiveObservedAt(event.OccurredAt, s.Now),
		CreatedAt:         s.Now().UTC(),
	}

	if err := s.Repos.ChannelEvents.Insert(ctx, stored); err != nil {
		if sqlitestore.IsUniqueConstraint(err) {
			s.Logger.Info(
				"runtime: ignoring duplicate channel event",
				"profile_id", event.ProfileID,
				"provider", event.Provider,
				"event_id", stored.EventID,
				"event_type", stored.Type,
			)
			return nil
		}
		return err
	}

	s.Logger.Info(
		"runtime: observed channel event",
		"profile_id", event.ProfileID,
		"provider", event.Provider,
		"event_id", stored.EventID,
		"event_type", stored.Type,
		"room_id", event.RoomID,
		"message_id", event.ProviderMessageID,
	)

	if event.Provider == domain.ProviderFeishu && strings.TrimSpace(event.Type) == "im.message.recalled_v1" {
		return s.syncFeishuRecalledMessage(ctx, event)
	}
	return nil
}

func (s *ChannelEventService) syncFeishuRecalledMessage(
	ctx context.Context,
	event channelcore.ChannelEvent,
) error {
	if strings.TrimSpace(event.ProviderMessageID) == "" {
		return nil
	}

	message, err := s.Repos.Messages.GetByProfileProviderMessageID(ctx, event.ProfileID, event.ProviderMessageID)
	if err != nil {
		if errors.Is(err, sqlitestore.ErrNotFound) {
			s.Logger.Info(
				"runtime: recall event references unknown message",
				"profile_id", event.ProfileID,
				"provider", event.Provider,
				"message_id", event.ProviderMessageID,
			)
			return nil
		}
		return err
	}

	nextRawJSON := mergeMessageEventRawJSON(message.RawJSON, event)
	if message.ContentText == recalledMessageText && strings.TrimSpace(message.RawJSON) == strings.TrimSpace(nextRawJSON) {
		return nil
	}

	if err := s.Repos.Messages.UpdateContentAndRawJSONByProfileProviderMessageID(
		ctx,
		message.ProfileID,
		message.ProviderMessageID,
		recalledMessageText,
		nextRawJSON,
	); err != nil {
		return err
	}

	if _, err := s.ChatService.RefreshRoomSessionSummary(ctx, message.ProfileID, message.RoomID); err != nil {
		return err
	}

	s.Logger.Info(
		"runtime: synced recalled message",
		"profile_id", message.ProfileID,
		"provider", event.Provider,
		"room_id", message.RoomID,
		"message_id", message.ProviderMessageID,
	)
	return nil
}

func buildChannelEventID(event channelcore.ChannelEvent, now func() time.Time) string {
	provider := strings.TrimSpace(string(event.Provider))
	if provider == "" {
		provider = "unknown"
	}
	return "event:" + provider + ":" + string(event.ProfileID) + ":" + fallbackEventID(event, now)
}

func fallbackEventID(event channelcore.ChannelEvent, now func() time.Time) string {
	if value := strings.TrimSpace(event.EventID); value != "" {
		return value
	}
	if value := strings.TrimSpace(event.Type); value != "" && strings.TrimSpace(event.ProviderMessageID) != "" {
		return value + ":" + strings.TrimSpace(event.ProviderMessageID)
	}
	return fmt.Sprintf("%d", now().UTC().UnixNano())
}

func effectiveObservedAt(value time.Time, now func() time.Time) time.Time {
	if !value.IsZero() {
		return value.UTC()
	}
	return now().UTC()
}

func normalizePayloadJSON(payload []byte) string {
	trimmed := strings.TrimSpace(string(payload))
	if trimmed == "" {
		return "{}"
	}
	return trimmed
}

func mergeMessageEventRawJSON(existing string, event channelcore.ChannelEvent) string {
	state := map[string]any{}
	if trimmed := strings.TrimSpace(existing); trimmed != "" && trimmed != "{}" {
		_ = json.Unmarshal([]byte(trimmed), &state)
	}

	state["state"] = "recalled"
	state["recalled_event_id"] = strings.TrimSpace(event.EventID)
	state["recalled_event_type"] = strings.TrimSpace(event.Type)
	if !event.OccurredAt.IsZero() {
		state["recalled_at"] = event.OccurredAt.UTC().Format(time.RFC3339Nano)
	}

	if trimmedPayload := strings.TrimSpace(string(event.PayloadJSON)); trimmedPayload != "" && trimmedPayload != "{}" {
		var payload any
		if json.Unmarshal(event.PayloadJSON, &payload) == nil {
			state["recalled_event"] = payload
		}
	}

	encoded, err := json.Marshal(state)
	if err != nil {
		return `{"state":"recalled"}`
	}
	return string(encoded)
}
