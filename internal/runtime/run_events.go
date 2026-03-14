package runtime

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"goclaw/internal/domain"
)

type RunEventType string

const (
	RunEventRunStarted       RunEventType = "run_started"
	RunEventAssistantStarted RunEventType = "assistant_started"
	RunEventReasoningDelta   RunEventType = "reasoning_delta"
	RunEventAssistantDelta   RunEventType = "assistant_delta"
	RunEventAssistantBlock   RunEventType = "assistant_block"
	RunEventToolCallStarted  RunEventType = "tool_call_started"
	RunEventToolCallFinished RunEventType = "tool_call_finished"
	RunEventToolResult       RunEventType = "tool_result"
	RunEventFinalText        RunEventType = "final_text"
	RunEventRunCompleted     RunEventType = "run_completed"
	RunEventRunFailed        RunEventType = "run_failed"
)

type RunAssistantBlockKind string

const (
	RunAssistantBlockToolCall RunAssistantBlockKind = "tool_call"
)

type RunEvent struct {
	RunID            string
	Sequence         int64
	Type             RunEventType
	Provider         domain.Provider
	ProfileID        domain.ProfileID
	RoomID           domain.RoomID
	ConversationID   domain.ConversationID
	RequestMessageID string
	Iteration        int
	ToolCallID       string
	ToolName         string
	ToolArgsJSON     string
	ToolResultJSON   string
	NativeToolCall   bool
	AssistantBlock   RunAssistantBlockKind
	Text             string
	ErrorText        string
	OccurredAt       time.Time
}

type RunObserver interface {
	ObserveRunEvent(context.Context, RunEvent) error
}

type runEventObservers struct {
	logger      *slog.Logger
	roomContext RoomContext
	required    []RunObserver
	bestEffort  []RunObserver
}

func newRunEventObservers(
	logger *slog.Logger,
	roomContext RoomContext,
	required []RunObserver,
	bestEffort []RunObserver,
) runEventObservers {
	if logger == nil {
		logger = slog.Default()
	}
	return runEventObservers{
		logger:      logger,
		roomContext: roomContext,
		required:    filterRunObservers(required),
		bestEffort:  filterRunObservers(bestEffort),
	}
}

func newRunEventObserversForSnapshot(
	logger *slog.Logger,
	snapshot RunSnapshot,
	required []RunObserver,
	bestEffort []RunObserver,
) runEventObservers {
	return newRunEventObservers(logger, snapshot.RoomContext(), required, bestEffort)
}

func (o runEventObservers) Emit(ctx context.Context, event RunEvent) error {
	for _, observer := range o.required {
		if err := observer.ObserveRunEvent(ctx, event); err != nil {
			return err
		}
	}
	for _, observer := range o.bestEffort {
		if err := observer.ObserveRunEvent(ctx, event); err != nil {
			o.logger.Warn(
				"runtime: run observer failed",
				"profile_id", o.roomContext.Profile.ID,
				"room_id", o.roomContext.Room.ID,
				"provider", o.roomContext.Room.Provider,
				"event_type", event.Type,
				"error", err,
			)
		}
	}
	return nil
}

func filterRunObservers(observers []RunObserver) []RunObserver {
	if len(observers) == 0 {
		return nil
	}
	filtered := make([]RunObserver, 0, len(observers))
	for _, observer := range observers {
		if observer == nil {
			continue
		}
		filtered = append(filtered, observer)
	}
	return filtered
}

type runEventFactory struct {
	roomContext RoomContext
	now         func() time.Time
	runID       string
	sequence    int64
}

func newRunEventFactory(roomContext RoomContext, now func() time.Time) *runEventFactory {
	if now == nil {
		now = time.Now
	}
	return &runEventFactory{
		roomContext: roomContext,
		now:         now,
		runID:       buildReplyRunID(roomContext.Profile.ID, roomContext.Room.ID, roomContext.TriggerMessageID, now),
	}
}

func newRunEventFactoryWithRunID(
	roomContext RoomContext,
	now func() time.Time,
	runID string,
) *runEventFactory {
	factory := newRunEventFactory(roomContext, now)
	if strings.TrimSpace(runID) != "" {
		factory.runID = strings.TrimSpace(runID)
	}
	return factory
}

func newRunEventFactoryForSnapshot(
	snapshot RunSnapshot,
	now func() time.Time,
) *runEventFactory {
	return newRunEventFactoryWithRunID(snapshot.RoomContext(), now, snapshot.RunID)
}

func (f *runEventFactory) New(eventType RunEventType) RunEvent {
	f.sequence++
	return RunEvent{
		RunID:            f.runID,
		Sequence:         f.sequence,
		Type:             eventType,
		Provider:         f.roomContext.Room.Provider,
		ProfileID:        f.roomContext.Profile.ID,
		RoomID:           f.roomContext.Room.ID,
		ConversationID:   f.roomContext.Conversation.ID,
		RequestMessageID: strings.TrimSpace(f.roomContext.TriggerProviderMessageID),
		OccurredAt:       runEventNow(f.now),
	}
}

func runEventNow(now func() time.Time) time.Time {
	if now == nil {
		return time.Now().UTC()
	}
	return now().UTC()
}

type runTextCollector struct {
	assistantText string
	finalText     string
}

func (c *runTextCollector) ObserveRunEvent(_ context.Context, event RunEvent) error {
	switch event.Type {
	case RunEventAssistantDelta:
		if event.Text == "" {
			return nil
		}
		c.assistantText += event.Text
	case RunEventFinalText:
		c.finalText = event.Text
	}
	return nil
}

func (c *runTextCollector) AssistantText() string {
	if c == nil {
		return ""
	}
	return c.assistantText
}

func (c *runTextCollector) ReplyText() string {
	if c == nil {
		return ""
	}
	return strings.TrimSpace(firstNonEmptyStreamingText(c.finalText, c.assistantText))
}

type pendingToolRunEvent struct {
	iteration    int
	toolCallID   string
	toolName     string
	toolArgsJSON string
	native       bool
}

type runEventEmitter struct {
	observers    runEventObservers
	factory      *runEventFactory
	pendingTools []pendingToolRunEvent
}

func newRunEventEmitter(
	logger *slog.Logger,
	roomContext RoomContext,
	now func() time.Time,
	runID string,
	required []RunObserver,
	bestEffort []RunObserver,
) *runEventEmitter {
	return &runEventEmitter{
		observers: newRunEventObservers(logger, roomContext, required, bestEffort),
		factory:   newRunEventFactoryWithRunID(roomContext, now, runID),
	}
}

func newRunEventEmitterForSnapshot(
	logger *slog.Logger,
	snapshot RunSnapshot,
	now func() time.Time,
	required []RunObserver,
	bestEffort []RunObserver,
) *runEventEmitter {
	return &runEventEmitter{
		observers: newRunEventObserversForSnapshot(logger, snapshot, required, bestEffort),
		factory:   newRunEventFactoryForSnapshot(snapshot, now),
	}
}

func newBestEffortRunEventEmitter(
	logger *slog.Logger,
	roomContext RoomContext,
	now func() time.Time,
	runID string,
	bestEffort []RunObserver,
) *runEventEmitter {
	return newRunEventEmitter(logger, roomContext, now, runID, nil, bestEffort)
}

func newBestEffortRunEventEmitterForSnapshot(
	logger *slog.Logger,
	snapshot RunSnapshot,
	now func() time.Time,
	bestEffort []RunObserver,
) *runEventEmitter {
	return newRunEventEmitterForSnapshot(logger, snapshot, now, nil, bestEffort)
}

func (e *runEventEmitter) Emit(ctx context.Context, event RunEvent) error {
	if e == nil {
		return nil
	}
	return e.observers.Emit(ctx, event)
}

func (e *runEventEmitter) EmitRunStarted(ctx context.Context) error {
	if e == nil {
		return nil
	}
	if err := e.Emit(ctx, e.factory.New(RunEventRunStarted)); err != nil {
		return err
	}
	return e.Emit(ctx, e.factory.New(RunEventAssistantStarted))
}

func (e *runEventEmitter) EmitAssistantBlock(
	ctx context.Context,
	blockKind RunAssistantBlockKind,
	text string,
	iteration int,
	toolCallID string,
	toolName string,
	toolArgsJSON string,
	native bool,
) error {
	if e == nil {
		return nil
	}
	if strings.TrimSpace(text) == "" && strings.TrimSpace(toolName) == "" {
		return nil
	}
	event := e.factory.New(RunEventAssistantBlock)
	event.AssistantBlock = blockKind
	event.Text = strings.TrimSpace(text)
	event.Iteration = iteration
	event.ToolCallID = strings.TrimSpace(toolCallID)
	event.ToolName = strings.TrimSpace(toolName)
	event.ToolArgsJSON = strings.TrimSpace(toolArgsJSON)
	event.NativeToolCall = native
	return e.Emit(ctx, event)
}

func (e *runEventEmitter) EmitAssistantDelta(ctx context.Context, text string) error {
	if e == nil || text == "" {
		return nil
	}
	event := e.factory.New(RunEventAssistantDelta)
	event.Text = text
	return e.Emit(ctx, event)
}

func (e *runEventEmitter) EmitFinalText(ctx context.Context, text string) error {
	if e == nil || strings.TrimSpace(text) == "" {
		return nil
	}
	event := e.factory.New(RunEventFinalText)
	event.Text = strings.TrimSpace(text)
	return e.Emit(ctx, event)
}

func (e *runEventEmitter) EmitRunFinished(
	ctx context.Context,
	finalText string,
	runErr error,
) error {
	if e == nil {
		return nil
	}
	if err := e.EmitFinalText(ctx, finalText); err != nil {
		return err
	}
	if runErr != nil {
		event := e.factory.New(RunEventRunFailed)
		event.ErrorText = runErr.Error()
		return e.Emit(ctx, event)
	}
	return e.Emit(ctx, e.factory.New(RunEventRunCompleted))
}

func (e *runEventEmitter) OnToolCallStarted(
	ctx context.Context,
	iteration int,
	toolCallID string,
	toolName string,
	toolArgsJSON string,
	assistantBlockText string,
	native bool,
) error {
	if e == nil {
		return nil
	}
	if err := e.EmitAssistantBlock(
		ctx,
		RunAssistantBlockToolCall,
		assistantBlockText,
		iteration,
		toolCallID,
		toolName,
		toolArgsJSON,
		native,
	); err != nil {
		return err
	}
	e.pendingTools = append(e.pendingTools, pendingToolRunEvent{
		iteration:    iteration,
		toolCallID:   strings.TrimSpace(toolCallID),
		toolName:     strings.TrimSpace(toolName),
		toolArgsJSON: strings.TrimSpace(toolArgsJSON),
		native:       native,
	})
	event := e.factory.New(RunEventToolCallStarted)
	event.Iteration = iteration
	event.ToolCallID = strings.TrimSpace(toolCallID)
	event.ToolName = strings.TrimSpace(toolName)
	event.ToolArgsJSON = strings.TrimSpace(toolArgsJSON)
	event.NativeToolCall = native
	return e.Emit(ctx, event)
}

func (e *runEventEmitter) OnToolCallFinished(
	ctx context.Context,
	toolResultJSON string,
	invocationErr error,
) error {
	if e == nil {
		return nil
	}
	if len(e.pendingTools) == 0 {
		return nil
	}
	pending := e.pendingTools[0]
	e.pendingTools = e.pendingTools[1:]

	resultEvent := e.factory.New(RunEventToolResult)
	resultEvent.Iteration = pending.iteration
	resultEvent.ToolCallID = pending.toolCallID
	resultEvent.ToolName = pending.toolName
	resultEvent.ToolArgsJSON = pending.toolArgsJSON
	resultEvent.ToolResultJSON = strings.TrimSpace(toolResultJSON)
	resultEvent.NativeToolCall = pending.native
	if invocationErr != nil {
		resultEvent.ErrorText = invocationErr.Error()
	}
	if err := e.Emit(ctx, resultEvent); err != nil {
		return err
	}

	finishedEvent := e.factory.New(RunEventToolCallFinished)
	finishedEvent.Iteration = pending.iteration
	finishedEvent.ToolCallID = pending.toolCallID
	finishedEvent.ToolName = pending.toolName
	finishedEvent.ToolArgsJSON = pending.toolArgsJSON
	finishedEvent.ToolResultJSON = strings.TrimSpace(toolResultJSON)
	finishedEvent.NativeToolCall = pending.native
	if invocationErr != nil {
		finishedEvent.ErrorText = invocationErr.Error()
	}
	return e.Emit(ctx, finishedEvent)
}
