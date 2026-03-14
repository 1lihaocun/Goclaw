package runtime

import (
	"context"
	"strings"
	"time"

	channelcore "goclaw/internal/channels"
	"goclaw/internal/domain"
	"goclaw/internal/model"
)

const streamingSessionCloseTimeout = 5 * time.Second

func (s *ReplyService) streamChannelReply(
	ctx context.Context,
	outbound channelcore.Outbound,
	snapshot RunSnapshot,
) (string, channelcore.SendResult, bool, error) {
	plan := snapshot.ReplyPlan
	if snapshot.ToolingMode != toolingModeDisabled {
		return "", channelcore.SendResult{}, false, nil
	}

	streamProvider, ok := model.StreamProviderFor(s.Model)
	if !ok {
		return "", channelcore.SendResult{}, false, nil
	}
	streamingOutbound, ok := outbound.(channelcore.StreamingReplyOutbound)
	if !ok {
		return "", channelcore.SendResult{}, false, nil
	}

	session, err := streamingOutbound.BeginStreamingReply(ctx, channelcore.StreamingReplyTarget{
		RoomID:           string(snapshot.Room.ID),
		ReplyToMessageID: strings.TrimSpace(snapshot.TriggerProviderMessageID),
	})
	if err != nil {
		s.Logger.Warn(
			"runtime: failed to start streaming reply session",
			"profile_id", snapshot.Profile.ID,
			"room_id", snapshot.Room.ID,
			"provider", snapshot.Room.Provider,
			"error", err,
		)
		return "", channelcore.SendResult{}, false, nil
	}

	projector := NewReplyProjector(s.replyProjectorConfig(snapshot))
	observers := newRunEventObserversForSnapshot(
		s.Logger,
		snapshot,
		[]RunObserver{
			streamingReplyObserver{
				session:   session,
				projector: projector,
				chunker:   NewReplyBlockChunker(),
			},
		},
		s.RunObservers,
	)
	eventFactory := newRunEventFactoryForSnapshot(snapshot, s.Now)
	emitEvent := func(event RunEvent) error {
		return observers.Emit(ctx, event)
	}
	if err := emitEvent(eventFactory.New(RunEventRunStarted)); err != nil {
		_ = s.closeStreamingSession(session, "")
		return "", channelcore.SendResult{}, true, err
	}
	if err := emitEvent(eventFactory.New(RunEventAssistantStarted)); err != nil {
		_ = s.closeStreamingSession(session, "")
		return "", channelcore.SendResult{}, true, err
	}

	finalText, streamErr := streamProvider.Stream(ctx, plan.Request, func(event model.StreamEvent) error {
		if event.Type != model.StreamEventAssistantDelta || event.Text == "" {
			return nil
		}
		runEvent := eventFactory.New(RunEventAssistantDelta)
		runEvent.Text = event.Text
		return emitEvent(runEvent)
	})
	if streamErr != nil && ctx.Err() == nil {
		s.Logger.Warn(
			"runtime: provider stream failed, falling back to generate",
			"profile_id", snapshot.Profile.ID,
			"room_id", snapshot.Room.ID,
			"provider", snapshot.Room.Provider,
			"model_provider", s.Model.Name(),
			"error", streamErr,
		)
		fallbackText, fallbackErr := s.Model.Generate(ctx, plan.Request)
		if fallbackErr == nil {
			finalText = fallbackText
			streamErr = nil
		}
	}

	replyText := strings.TrimSpace(firstNonEmptyStreamingText(finalText, projector.CurrentText()))
	if replyText != "" {
		finalEvent := eventFactory.New(RunEventFinalText)
		finalEvent.Text = replyText
		if err := emitEvent(finalEvent); err != nil {
			streamErr = err
		}
	}
	closeErr := s.closeStreamingSession(session, replyText)
	if streamErr == nil && closeErr == nil {
		_ = emitEvent(eventFactory.New(RunEventRunCompleted))
	} else {
		failedEvent := eventFactory.New(RunEventRunFailed)
		switch {
		case closeErr != nil && streamErr == nil:
			failedEvent.ErrorText = closeErr.Error()
		case streamErr != nil:
			failedEvent.ErrorText = streamErr.Error()
		}
		_ = emitEvent(failedEvent)
	}
	if closeErr != nil && streamErr == nil {
		return "", channelcore.SendResult{}, true, closeErr
	}
	if streamErr != nil {
		return replyText, session.SendResult(), true, streamErr
	}
	return replyText, session.SendResult(), true, nil
}

func (s *ReplyService) projectChannelReply(
	ctx context.Context,
	outbound channelcore.Outbound,
	snapshot RunSnapshot,
) (string, channelcore.SendResult, bool, error) {
	if snapshot.ToolingMode == toolingModeDisabled {
		return "", channelcore.SendResult{}, false, nil
	}
	streamingOutbound, ok := outbound.(channelcore.StreamingReplyOutbound)
	if !ok {
		return "", channelcore.SendResult{}, false, nil
	}

	session, err := streamingOutbound.BeginStreamingReply(ctx, channelcore.StreamingReplyTarget{
		RoomID:           string(snapshot.Room.ID),
		ReplyToMessageID: strings.TrimSpace(snapshot.TriggerProviderMessageID),
	})
	if err != nil {
		s.Logger.Warn(
			"runtime: failed to start projected reply session",
			"profile_id", snapshot.Profile.ID,
			"room_id", snapshot.Room.ID,
			"provider", snapshot.Room.Provider,
			"error", err,
		)
		return "", channelcore.SendResult{}, false, nil
	}

	projector := NewReplyProjector(s.replyProjectorConfig(snapshot))
	replyObserver := streamingReplyObserver{
		session:   session,
		projector: projector,
		chunker:   NewReplyBlockChunker(),
	}
	replyText, runErr := s.generateReplyTextFromSnapshotWithObservers(
		ctx,
		snapshot,
		[]RunObserver{replyObserver},
	)
	replyText = strings.TrimSpace(firstNonEmptyStreamingText(replyText, projector.CurrentText()))

	closeErr := s.closeStreamingSession(session, replyText)
	if closeErr != nil && runErr == nil {
		return "", channelcore.SendResult{}, true, closeErr
	}
	if runErr != nil {
		return replyText, session.SendResult(), true, runErr
	}
	return replyText, session.SendResult(), true, nil
}

type streamingReplyObserver struct {
	session   channelcore.StreamingReplySession
	projector *ReplyProjector
	chunker   *ReplyBlockChunker
}

func (o streamingReplyObserver) ObserveRunEvent(
	ctx context.Context,
	event RunEvent,
) error {
	if o.session == nil || o.projector == nil {
		return nil
	}
	deliveries, err := o.projector.OnRunEvent(event)
	if err != nil {
		return err
	}
	for _, delivery := range deliveries {
		switch delivery.Kind {
		case ReplyDeliveryText:
			text := delivery.Text
			if o.chunker != nil {
				chunkedText, ok := o.chunker.OnText(text)
				if !ok {
					continue
				}
				text = chunkedText
			}
			if text == "" {
				continue
			}
			if err := o.session.UpdateText(ctx, text); err != nil {
				return err
			}
		case ReplyDeliveryTool:
			toolSession, ok := o.session.(channelcore.ToolStreamingReplySession)
			if !ok {
				continue
			}
			toolText := renderToolDeliveryText(delivery)
			if strings.TrimSpace(toolText) == "" {
				continue
			}
			if err := toolSession.UpdateToolMessage(ctx, delivery.ToolCallID, toolText, delivery.AllowEdit); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *ReplyService) replyProjectorConfig(snapshot RunSnapshot) ReplyProjectorConfig {
	return ReplyProjectorConfig{
		ToolLifecycleVisibility: s.toolLifecycleVisibility(snapshot.Room.Provider),
		RoomKind:                snapshot.Room.Kind,
	}
}

func (s *ReplyService) toolLifecycleVisibility(provider domain.Provider) string {
	if provider != domain.ProviderFeishu {
		return "off"
	}
	return normalizeToolLifecycleVisibility(s.FeishuStreamingToolSummaries)
}

func renderToolDeliveryText(delivery ReplyDelivery) string {
	toolName := strings.TrimSpace(delivery.ToolName)
	if toolName == "" {
		toolName = "tool"
	}
	switch strings.TrimSpace(delivery.ToolStatus) {
	case "started":
		return "Using tool: " + toolName
	case "result":
		if strings.TrimSpace(delivery.ErrorText) != "" {
			return "Tool " + toolName + " returned an error"
		}
		return "Tool " + toolName + " returned a result"
	case "finished":
		if strings.TrimSpace(delivery.ErrorText) != "" {
			return "Tool " + toolName + " failed"
		}
		return "Tool " + toolName + " finished"
	default:
		return ""
	}
}

func (s *ReplyService) closeStreamingSession(
	session channelcore.StreamingReplySession,
	finalText string,
) error {
	if session == nil {
		return nil
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), streamingSessionCloseTimeout)
	defer cancel()
	return session.Close(closeCtx, finalText)
}

func firstNonEmptyStreamingText(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
