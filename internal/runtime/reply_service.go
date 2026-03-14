package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"goclaw/internal/agenttools"
	channelcore "goclaw/internal/channels"
	"goclaw/internal/config"
	"goclaw/internal/domain"
	"goclaw/internal/feishu"
	"goclaw/internal/model"
	sqlitestore "goclaw/internal/store/sqlite"
	toolruntime "goclaw/internal/tools"
)

const (
	assistantPersonPrefix = "assistant:"
	memoryWriteTimeout    = 3 * time.Second
	processingAckTimeout  = 5 * time.Second
)

type ChannelOutbound interface {
	ProfileID() domain.ProfileID
	Provider() domain.Provider
	Configured() bool
	SendText(ctx context.Context, chatID, text string) (channelcore.SendResult, error)
}

type replyTextOutbound interface {
	SendReplyText(ctx context.Context, roomID, replyToMessageID, text string) (channelcore.SendResult, error)
}

type outboundResolver interface {
	Outbound(provider domain.Provider, profileID domain.ProfileID) (channelcore.Outbound, bool)
}

type ReplyService struct {
	ChatService                  *ChatService
	PromptBuilder                *PromptBuilder
	Model                        model.Provider
	Outbounds                    outboundResolver
	Tools                        *toolruntime.LocalRuntime
	AgentTools                   *agenttools.Registry
	RunObservers                 []RunObserver
	FeishuStreamingToolSummaries string
	LegacyToolFallbackEnabled    bool
	ToolMaxIterations            int
	Repos                        sqlitestore.Repositories
	Now                          func() time.Time
	Logger                       *slog.Logger
}

func NewReplyService(
	repos sqlitestore.Repositories,
	promptBuilder *PromptBuilder,
	modelProvider model.Provider,
	outbounds outboundResolver,
) *ReplyService {
	return &ReplyService{
		ChatService:                  NewChatService(repos),
		PromptBuilder:                promptBuilder,
		Model:                        modelProvider,
		Outbounds:                    outbounds,
		Tools:                        toolruntime.NewLocalRuntime(repos.ToolPermissions, nil),
		FeishuStreamingToolSummaries: config.Default().Channels.Feishu.StreamingToolSummaries,
		LegacyToolFallbackEnabled:    false,
		ToolMaxIterations:            config.Default().Tooling.MaxIterations,
		Repos:                        repos,
		Now:                          time.Now,
		Logger:                       slog.Default(),
	}
}

func (s *ReplyService) IngestInboundMessage(
	ctx context.Context,
	message channelcore.InboundMessage,
) error {
	roomContext, err := s.ChatService.RecordInboundMessage(ctx, message)
	if err != nil {
		if errors.Is(err, ErrDuplicateMessage) {
			s.Logger.Info(
				"runtime: ignoring duplicate inbound message",
				"profile_id", message.ProfileID,
				"provider", message.Provider,
				"room_id", message.RoomID,
				"message_id", message.ProviderMessageID,
			)
			return nil
		}
		return err
	}
	if !s.replyEnabled(roomContext.Room.Provider, roomContext.Profile.ID) {
		return nil
	}
	outbound, ok := s.outbound(roomContext.Room.Provider, roomContext.Profile.ID)
	if !ok {
		return fmt.Errorf(
			"runtime: missing outbound for provider %q profile %q",
			roomContext.Room.Provider,
			roomContext.Profile.ID,
		)
	}
	cleanupProcessingAck := s.beginProcessingAck(ctx, roomContext, outbound)
	if cleanupProcessingAck != nil {
		defer cleanupProcessingAck()
	}

	promptPackage, err := s.PromptBuilder.Build(ctx, PromptBuildInput{
		RoomContext: roomContext,
	})
	if err != nil {
		return err
	}
	runSnapshot, err := s.buildRunSnapshot(ctx, roomContext, promptPackage)
	if err != nil {
		return err
	}

	replyText, sendResult, streamed, err := s.streamChannelReply(ctx, outbound, runSnapshot)
	if !streamed {
		replyText, sendResult, streamed, err = s.projectChannelReply(ctx, outbound, runSnapshot)
	}
	if !streamed {
		replyText, err = s.generateReplyTextFromSnapshot(ctx, runSnapshot)
		if err != nil {
			return err
		}
		replyText = strings.TrimSpace(replyText)
		if replyText == "" {
			return nil
		}

		sendResult, err = s.sendChannelReply(ctx, outbound, runSnapshot, replyText)
		if err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else {
		replyText = strings.TrimSpace(replyText)
		if replyText == "" {
			return nil
		}
	}
	s.Logger.Info(
		"runtime: sent channel reply",
		"profile_id", runSnapshot.Profile.ID,
		"provider", outbound.Provider(),
		"room_id", runSnapshot.Room.ID,
		"request_message_id", message.ProviderMessageID,
		"reply_message_id", sendResult.MessageID,
		"model_provider", s.Model.Name(),
	)

	assistantPerson, err := s.ensureAssistantPerson(ctx, runSnapshot.Profile.ID, runSnapshot.Room.Provider)
	if err != nil {
		return err
	}

	replyCreatedAt := s.Now().UTC().Add(time.Nanosecond)
	assistantMessage := domain.Message{
		ID:                buildAssistantMessageID(runSnapshot.Profile.ID, runSnapshot.Room.Provider, sendResult.MessageID, s.Now),
		ProfileID:         runSnapshot.Profile.ID,
		RoomID:            runSnapshot.Room.ID,
		ConversationID:    runSnapshot.Conversation.ID,
		PersonID:          assistantPerson.ID,
		SessionID:         runSnapshot.Session.ID,
		ProviderMessageID: sendResult.MessageID,
		Role:              domain.MessageRoleAssistant,
		ContentText:       replyText,
		// Keep assistant replies strictly after the triggering user message so
		// transcript ordering stays deterministic even with fixed clocks.
		CreatedAt: replyCreatedAt,
	}
	if err := s.Repos.Messages.Insert(ctx, assistantMessage); err != nil {
		return err
	}

	if _, err := s.ChatService.RefreshRoomSessionSummary(ctx, runSnapshot.Profile.ID, runSnapshot.Room.ID); err != nil {
		return err
	}

	enqueueMemoryJobsBestEffort(
		s.Logger,
		s.Repos,
		s.PromptBuilder.Workspace,
		s.Now,
		runSnapshot.RoomContext(),
		runSnapshot.Prompt,
		assistantMessage,
	)
	return nil
}

func (s *ReplyService) sendChannelReply(
	ctx context.Context,
	outbound channelcore.Outbound,
	snapshot RunSnapshot,
	replyText string,
) (channelcore.SendResult, error) {
	replyToMessageID := strings.TrimSpace(snapshot.TriggerProviderMessageID)
	if replyingOutbound, ok := outbound.(replyTextOutbound); ok && replyToMessageID != "" {
		return replyingOutbound.SendReplyText(ctx, string(snapshot.Room.ID), replyToMessageID, replyText)
	}
	return outbound.SendText(ctx, string(snapshot.Room.ID), replyText)
}

func (s *ReplyService) beginProcessingAck(
	ctx context.Context,
	roomContext RoomContext,
	outbound channelcore.Outbound,
) func() {
	ackOutbound, ok := outbound.(channelcore.ProcessingAckOutbound)
	if !ok {
		return nil
	}
	messageID := strings.TrimSpace(roomContext.TriggerProviderMessageID)
	if messageID == "" {
		return nil
	}
	handle, err := ackOutbound.BeginProcessingAck(ctx, channelcore.ProcessingAckTarget{
		RoomID:    string(roomContext.Room.ID),
		MessageID: messageID,
	})
	if err != nil {
		s.Logger.Warn(
			"runtime: failed to add processing ack",
			"profile_id", roomContext.Profile.ID,
			"provider", roomContext.Room.Provider,
			"room_id", roomContext.Room.ID,
			"message_id", messageID,
			"error", err,
		)
		return nil
	}
	if strings.TrimSpace(handle.AckID) == "" {
		return nil
	}
	return func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), processingAckTimeout)
		defer cancel()
		if err := ackOutbound.EndProcessingAck(cleanupCtx, handle); err != nil {
			s.Logger.Warn(
				"runtime: failed to remove processing ack",
				"profile_id", roomContext.Profile.ID,
				"provider", roomContext.Room.Provider,
				"room_id", roomContext.Room.ID,
				"message_id", messageID,
				"ack_id", handle.AckID,
				"error", err,
			)
		}
	}
}

func (s *ReplyService) IngestFeishuMessageEvent(
	ctx context.Context,
	profileID domain.ProfileID,
	event feishu.MessageEvent,
) error {
	message, err := feishu.NormalizeMessageEvent(profileID, event)
	if err != nil {
		return err
	}
	return s.IngestInboundMessage(ctx, channelcore.InboundMessage{
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

func (s *ReplyService) replyEnabled(provider domain.Provider, profileID domain.ProfileID) bool {
	outbound, ok := s.outbound(provider, profileID)
	return s.ChatService != nil &&
		s.PromptBuilder != nil &&
		s.Model != nil &&
		s.Model.Name() != "noop" &&
		ok &&
		outbound != nil &&
		outbound.Configured()
}

func (s *ReplyService) outbound(
	provider domain.Provider,
	profileID domain.ProfileID,
) (channelcore.Outbound, bool) {
	if s.Outbounds == nil {
		return nil, false
	}
	return s.Outbounds.Outbound(provider, profileID)
}

func (s *ReplyService) ensureAssistantPerson(
	ctx context.Context,
	profileID domain.ProfileID,
	provider domain.Provider,
) (domain.Person, error) {
	providerName := strings.TrimSpace(string(provider))
	if providerName == "" {
		providerName = "unknown"
	}
	person := domain.Person{
		ID:             domain.PersonID(assistantPersonPrefix + providerName + ":" + string(profileID)),
		Provider:       provider,
		ProviderUserID: assistantPersonPrefix + providerName + ":" + string(profileID),
		DisplayName:    "GoClaw",
	}
	if err := s.Repos.Persons.Upsert(ctx, person); err != nil {
		return domain.Person{}, err
	}
	return person, nil
}

func buildAssistantMessageID(
	profileID domain.ProfileID,
	provider domain.Provider,
	providerMessageID string,
	now func() time.Time,
) domain.MessageID {
	providerName := strings.TrimSpace(string(provider))
	if providerName == "" {
		providerName = "unknown"
	}
	if strings.TrimSpace(providerMessageID) != "" {
		return domain.MessageID(fmt.Sprintf("msg:%s:assistant:%s", providerName, providerMessageID))
	}
	return domain.MessageID(fmt.Sprintf(
		"msg:%s:assistant:%s:%d",
		providerName,
		profileID,
		now().UTC().UnixNano(),
	))
}

func toModelRequest(promptPackage PromptPackage) model.Request {
	request := model.Request{
		System: promptPackage.RenderSystemPrompt(),
	}
	request.Messages = make([]model.Message, 0, len(promptPackage.Messages))
	for _, message := range promptPackage.Messages {
		request.Messages = append(request.Messages, model.Message{
			Role:    string(message.Role),
			Content: message.Content,
		})
	}
	return request
}

func lastPromptContentByRole(messages []PromptMessage, role domain.MessageRole) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != role {
			continue
		}
		content := strings.TrimSpace(messages[i].Content)
		if content != "" {
			return content
		}
	}
	return ""
}
