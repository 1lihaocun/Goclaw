package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	channelcore "goclaw/internal/channels"
	"goclaw/internal/domain"
	"goclaw/internal/feishu"
	sqlitestore "goclaw/internal/store/sqlite"
)

const (
	defaultRoomSessionStatus = "active"
	defaultProfileName       = "Default Profile"
	defaultSummaryEntryLimit = 16
	defaultSummaryTextLimit  = 160
)

var ErrDuplicateMessage = errors.New("runtime: duplicate inbound message")

type RoomContext struct {
	Profile                  domain.Profile
	Person                   domain.Person
	Room                     domain.Room
	Conversation             domain.Conversation
	ConversationSettings     domain.ConversationSettings
	Session                  domain.RoomSession
	Consent                  domain.ConsentPolicy
	TriggerMessageID         domain.MessageID
	TriggerProviderMessageID string
}

type ChatService struct {
	Repos               sqlitestore.Repositories
	Now                 func() time.Time
	RecentWindow        int
	SummaryEntryLimit   int
	SummaryContentLimit int
}

func NewChatService(repos sqlitestore.Repositories) *ChatService {
	return &ChatService{
		Repos:               repos,
		Now:                 time.Now,
		RecentWindow:        defaultTranscriptLimit,
		SummaryEntryLimit:   defaultSummaryEntryLimit,
		SummaryContentLimit: defaultSummaryTextLimit,
	}
}

func (s *ChatService) RecordInboundMessage(
	ctx context.Context,
	message channelcore.InboundMessage,
) (RoomContext, error) {
	profile := domain.Profile{
		ID:   message.ProfileID,
		Name: defaultProfileName,
	}
	if err := s.Repos.Profiles.Upsert(ctx, profile); err != nil {
		return RoomContext{}, err
	}

	person := domain.Person{
		ID:             message.PersonID,
		Provider:       message.Provider,
		ProviderUserID: fallbackName(message.ProviderUserID, string(message.PersonID)),
		DisplayName:    fallbackName(message.PersonName, string(message.PersonID)),
	}
	if err := s.Repos.Persons.Upsert(ctx, person); err != nil {
		return RoomContext{}, err
	}

	room := domain.Room{
		ID:             message.RoomID,
		Provider:       message.Provider,
		ProviderRoomID: fallbackName(message.ProviderRoomID, string(message.RoomID)),
		Kind:           effectiveRoomKind(message.RoomKind),
		Name:           fallbackName(message.RoomName, string(message.RoomID)),
	}
	if err := s.Repos.Rooms.Upsert(ctx, room); err != nil {
		return RoomContext{}, err
	}

	session, err := s.ensureRoomSession(ctx, profile.ID, room.Provider, room.ID, message.ReceivedAt)
	if err != nil {
		return RoomContext{}, err
	}
	conversation, conversationSettings, session, err := s.ensureActiveConversation(ctx, session, profile.ID, room.ID)
	if err != nil {
		return RoomContext{}, err
	}

	messageID := buildMessageID(message)
	if err := s.Repos.Messages.Insert(ctx, domain.Message{
		ID:                messageID,
		ProfileID:         profile.ID,
		RoomID:            room.ID,
		ConversationID:    conversation.ID,
		PersonID:          person.ID,
		SessionID:         session.ID,
		ProviderMessageID: fallbackName(message.ProviderMessageID, string(messageID)),
		Role:              domain.MessageRoleUser,
		ContentText:       message.ContentText,
		CreatedAt:         effectiveReceivedAt(message.ReceivedAt, s.Now),
	}); err != nil {
		if sqlitestore.IsUniqueConstraint(err) {
			return RoomContext{}, ErrDuplicateMessage
		}
		return RoomContext{}, err
	}

	session, err = s.RefreshRoomSessionSummary(ctx, profile.ID, room.ID)
	if err != nil {
		return RoomContext{}, err
	}

	consent, err := s.resolveConsent(ctx, profile.ID, room, person.ID)
	if err != nil {
		return RoomContext{}, err
	}

	return RoomContext{
		Profile:                  profile,
		Person:                   person,
		Room:                     room,
		Conversation:             conversation,
		ConversationSettings:     conversationSettings,
		Session:                  session,
		Consent:                  consent,
		TriggerMessageID:         messageID,
		TriggerProviderMessageID: strings.TrimSpace(message.ProviderMessageID),
	}, nil
}

func (s *ChatService) RecordFeishuMessageEvent(
	ctx context.Context,
	profileID domain.ProfileID,
	event feishu.MessageEvent,
) (RoomContext, error) {
	message, err := feishu.NormalizeMessageEvent(profileID, event)
	if err != nil {
		return RoomContext{}, err
	}
	return s.RecordInboundMessage(ctx, channelcore.InboundMessage{
		Provider:          domain.ProviderFeishu,
		ProfileID:         message.ProfileID,
		RoomID:            message.RoomID,
		RoomKind:          message.RoomKind,
		ProviderRoomID:    string(message.RoomID),
		PersonID:          message.PersonID,
		ProviderUserID:    string(message.PersonID),
		ProviderMessageID: message.ProviderMessageID,
		RoomName:          message.RoomName,
		PersonName:        message.PersonName,
		ContentText:       message.ContentText,
		ReceivedAt:        message.ReceivedAt,
		ChatType:          string(message.ChatType),
		ContentType:       string(message.ContentType),
		RootID:            message.RootID,
		ParentID:          message.ParentID,
		ThreadID:          message.ThreadID,
	})
}

func (s *ChatService) IngestFeishuMessageEvent(
	ctx context.Context,
	profileID domain.ProfileID,
	event feishu.MessageEvent,
) error {
	_, err := s.RecordFeishuMessageEvent(ctx, profileID, event)
	return err
}

func (s *ChatService) ensureRoomSession(
	ctx context.Context,
	profileID domain.ProfileID,
	provider domain.Provider,
	roomID domain.RoomID,
	receivedAt time.Time,
) (domain.RoomSession, error) {
	session, err := s.Repos.RoomSessions.GetByProfileAndRoom(ctx, profileID, roomID)
	if err == nil {
		session.LastMessageAt = effectiveReceivedAt(receivedAt, s.Now)
		if err := s.Repos.RoomSessions.Upsert(ctx, session); err != nil {
			return domain.RoomSession{}, err
		}
		return session, nil
	}
	if err != nil && !errors.Is(err, sqlitestore.ErrNotFound) {
		return domain.RoomSession{}, err
	}

	session = domain.RoomSession{
		ID:            buildRoomSessionID(profileID, provider, roomID),
		ProfileID:     profileID,
		RoomID:        roomID,
		Status:        defaultRoomSessionStatus,
		SummaryText:   "",
		LastMessageAt: effectiveReceivedAt(receivedAt, s.Now),
	}
	if err := s.Repos.RoomSessions.Upsert(ctx, session); err != nil {
		return domain.RoomSession{}, err
	}
	return session, nil
}

func (s *ChatService) RefreshRoomSessionSummary(
	ctx context.Context,
	profileID domain.ProfileID,
	roomID domain.RoomID,
) (domain.RoomSession, error) {
	session, err := s.Repos.RoomSessions.GetByProfileAndRoom(ctx, profileID, roomID)
	if err != nil {
		return domain.RoomSession{}, err
	}

	compactedMessages, err := s.Repos.Messages.ListCompactedBySession(ctx, session.ID, s.recentWindow())
	if err != nil {
		return domain.RoomSession{}, err
	}

	nextSummary := buildRoomSummary(
		compactedMessages,
		s.summaryEntryLimit(),
		s.summaryContentLimit(),
	)
	if session.SummaryText == nextSummary {
		return session, nil
	}

	session.SummaryText = nextSummary
	if err := s.Repos.RoomSessions.Upsert(ctx, session); err != nil {
		return domain.RoomSession{}, err
	}
	return session, nil
}

func (s *ChatService) ensureDefaultConversation(
	ctx context.Context,
	profileID domain.ProfileID,
	roomID domain.RoomID,
) (domain.Conversation, domain.ConversationSettings, error) {
	conversation, err := s.Repos.Conversations.GetByProfileRoomSlug(
		ctx,
		profileID,
		roomID,
		domain.DefaultConversationSlug,
	)
	if err != nil {
		if !errors.Is(err, sqlitestore.ErrNotFound) {
			return domain.Conversation{}, domain.ConversationSettings{}, err
		}
		conversation = domain.Conversation{
			ID:        buildConversationID(profileID, roomID, domain.DefaultConversationSlug),
			ProfileID: profileID,
			RoomID:    roomID,
			Slug:      domain.DefaultConversationSlug,
			Title:     "Default Conversation",
			Status:    domain.ConversationStatusActive,
		}
		if err := s.Repos.Conversations.Upsert(ctx, conversation); err != nil {
			return domain.Conversation{}, domain.ConversationSettings{}, err
		}
	}

	settings, err := s.Repos.ConversationSettings.Resolve(ctx, conversation.ID)
	if err != nil {
		return domain.Conversation{}, domain.ConversationSettings{}, err
	}
	return conversation, settings, nil
}

func (s *ChatService) ensureActiveConversation(
	ctx context.Context,
	session domain.RoomSession,
	profileID domain.ProfileID,
	roomID domain.RoomID,
) (domain.Conversation, domain.ConversationSettings, domain.RoomSession, error) {
	if session.ActiveConversationID != "" {
		conversation, err := s.Repos.Conversations.Get(ctx, session.ActiveConversationID)
		if err == nil {
			settings, err := s.Repos.ConversationSettings.Resolve(ctx, conversation.ID)
			if err != nil {
				return domain.Conversation{}, domain.ConversationSettings{}, domain.RoomSession{}, err
			}
			return conversation, settings, session, nil
		}
		if !errors.Is(err, sqlitestore.ErrNotFound) {
			return domain.Conversation{}, domain.ConversationSettings{}, domain.RoomSession{}, err
		}
		session.ActiveConversationID = ""
	}

	conversation, settings, err := s.ensureDefaultConversation(ctx, profileID, roomID)
	if err != nil {
		return domain.Conversation{}, domain.ConversationSettings{}, domain.RoomSession{}, err
	}
	if session.ActiveConversationID != conversation.ID {
		session.ActiveConversationID = conversation.ID
		if err := s.Repos.RoomSessions.Upsert(ctx, session); err != nil {
			return domain.Conversation{}, domain.ConversationSettings{}, domain.RoomSession{}, err
		}
	}
	return conversation, settings, session, nil
}

func (s *ChatService) resolveConsent(
	ctx context.Context,
	profileID domain.ProfileID,
	room domain.Room,
	personID domain.PersonID,
) (domain.ConsentPolicy, error) {
	return s.Repos.ConsentPolicies.Resolve(ctx, profileID, room.ID, personID)
}

func effectiveRoomKind(kind domain.RoomKind) domain.RoomKind {
	if kind == domain.RoomKindDirect {
		return domain.RoomKindDirect
	}
	return domain.RoomKindGroup
}

func buildRoomSessionID(
	profileID domain.ProfileID,
	provider domain.Provider,
	roomID domain.RoomID,
) domain.SessionID {
	providerName := strings.TrimSpace(string(provider))
	if providerName == "" {
		providerName = "unknown"
	}
	return domain.SessionID(fmt.Sprintf("session:%s:%s:%s", providerName, profileID, roomID))
}

func buildConversationID(
	profileID domain.ProfileID,
	roomID domain.RoomID,
	slug string,
) domain.ConversationID {
	return domain.ConversationID(fmt.Sprintf("conversation:%s:%s:%s", profileID, roomID, slug))
}

func buildMessageID(message channelcore.InboundMessage) domain.MessageID {
	provider := strings.TrimSpace(string(message.Provider))
	if provider == "" {
		provider = "unknown"
	}
	if message.ProviderMessageID != "" {
		return domain.MessageID(fmt.Sprintf("msg:%s:%s", provider, message.ProviderMessageID))
	}
	return domain.MessageID(fmt.Sprintf(
		"msg:%s:%s:%s:%d",
		provider,
		message.RoomID,
		message.PersonID,
		effectiveReceivedAt(message.ReceivedAt, time.Now).UnixNano(),
	))
}

func effectiveReceivedAt(value time.Time, now func() time.Time) time.Time {
	if value.IsZero() {
		return now().UTC()
	}
	return value.UTC()
}

func fallbackName(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func (s *ChatService) recentWindow() int {
	if s.RecentWindow <= 0 {
		return defaultTranscriptLimit
	}
	return s.RecentWindow
}

func (s *ChatService) summaryEntryLimit() int {
	if s.SummaryEntryLimit <= 0 {
		return defaultSummaryEntryLimit
	}
	return s.SummaryEntryLimit
}

func (s *ChatService) summaryContentLimit() int {
	if s.SummaryContentLimit <= 0 {
		return defaultSummaryTextLimit
	}
	return s.SummaryContentLimit
}

func buildRoomSummary(messages []domain.Message, entryLimit, contentLimit int) string {
	if len(messages) == 0 {
		return ""
	}

	lines := make([]string, 0, len(messages))
	for _, message := range messages {
		lines = append(lines, fmt.Sprintf(
			"- %s: %s",
			summarySpeaker(message),
			compactSummaryContent(message.ContentText, contentLimit),
		))
	}

	if omitted := len(lines) - entryLimit; omitted > 0 {
		lines = lines[omitted:]
		lines = append(
			[]string{fmt.Sprintf("%d earlier room message(s) omitted.", omitted)},
			lines...,
		)
	}

	return strings.Join(lines, "\n")
}

func summarySpeaker(message domain.Message) string {
	switch message.Role {
	case domain.MessageRoleAssistant:
		return "assistant"
	case domain.MessageRoleSystem:
		return "system"
	default:
		if strings.TrimSpace(string(message.PersonID)) == "" {
			return string(message.Role)
		}
		return fmt.Sprintf("%s/%s", message.Role, message.PersonID)
	}
}

func compactSummaryContent(content string, limit int) string {
	normalized := strings.Join(strings.Fields(content), " ")
	if normalized == "" {
		return "(empty)"
	}
	if limit <= 0 {
		limit = defaultSummaryTextLimit
	}

	runes := []rune(normalized)
	if len(runes) <= limit {
		return normalized
	}
	return strings.TrimSpace(string(runes[:limit])) + "..."
}
