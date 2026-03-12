package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"goclaw/internal/domain"
	"goclaw/internal/memory"
	sqlitestore "goclaw/internal/store/sqlite"
	workspacectx "goclaw/internal/workspace"
)

const (
	MemoryJobKindProviderWrite   = "provider_write"
	MemoryJobKindMarkdownWrite   = "markdown_write"
	MemoryJobKindMarkdownPromote = "markdown_promote"
	MemoryJobKindMarkdownReview  = "markdown_review"
	defaultMemoryJobBatchSize    = 16
	defaultMemoryJobPollDelay    = 2 * time.Second
	memoryQueueTimeout           = 2 * time.Second
)

type providerWriteJobPayload struct {
	Scope   memory.Scope         `json:"scope"`
	Records []memory.WriteRecord `json:"records"`
}

type markdownWriteJobPayload struct {
	Scope            memory.Scope         `json:"scope"`
	ConversationSlug string               `json:"conversation_slug"`
	Records          []memory.WriteRecord `json:"records"`
}

type DurablePromoteJobPayload struct {
	Scope            memory.Scope `json:"scope"`
	ConversationSlug string       `json:"conversation_slug"`
	Facts            []string     `json:"facts"`
	UpdatedBy        string       `json:"updated_by"`
}

type DurableReviewJobPayload struct {
	Scope            memory.Scope                   `json:"scope"`
	ConversationSlug string                         `json:"conversation_slug"`
	Facts            []string                       `json:"facts"`
	UpdatedBy        string                         `json:"updated_by"`
	Conflicts        []workspacectx.DurableConflict `json:"conflicts"`
}

type scopeWrite struct {
	scope   memory.Scope
	records []memory.WriteRecord
}

type MemoryJobWorker struct {
	Repos          sqlitestore.Repositories
	Memory         memory.Provider
	Workspace      *workspacectx.Loader
	Logger         *slog.Logger
	BatchSize      int
	PollInterval   time.Duration
	ProcessTimeout time.Duration
}

func NewMemoryJobWorker(
	repos sqlitestore.Repositories,
	provider memory.Provider,
	workspace *workspacectx.Loader,
	logger *slog.Logger,
) *MemoryJobWorker {
	return &MemoryJobWorker{
		Repos:          repos,
		Memory:         provider,
		Workspace:      workspace,
		Logger:         logger,
		BatchSize:      defaultMemoryJobBatchSize,
		PollInterval:   defaultMemoryJobPollDelay,
		ProcessTimeout: memoryWriteTimeout,
	}
}

func (w *MemoryJobWorker) Enabled() bool {
	return w != nil &&
		((w.Memory != nil && w.Memory.Name() != "noop") ||
			(w.Workspace != nil && w.Workspace.Enabled()))
}

func (w *MemoryJobWorker) Name() string {
	return "memory-jobs"
}

func (w *MemoryJobWorker) Run(ctx context.Context) error {
	if !w.Enabled() {
		return nil
	}
	if _, err := w.DrainOnce(ctx); err != nil {
		return err
	}

	interval := w.PollInterval
	if interval <= 0 {
		interval = defaultMemoryJobPollDelay
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if _, err := w.DrainOnce(ctx); err != nil {
				return err
			}
		}
	}
}

func (w *MemoryJobWorker) DrainOnce(ctx context.Context) (int, error) {
	if !w.Enabled() {
		return 0, nil
	}

	limit := w.BatchSize
	if limit <= 0 {
		limit = defaultMemoryJobBatchSize
	}

	jobs, err := w.Repos.MemoryJobs.ListPending(ctx, limit)
	if err != nil {
		return 0, err
	}

	processed := 0
	for _, job := range jobs {
		started, err := w.Repos.MemoryJobs.StartProcessing(ctx, job.ID)
		if err != nil {
			return processed, err
		}
		if !started {
			continue
		}

		if err := w.processJob(ctx, job); err != nil {
			if updateErr := w.Repos.MemoryJobs.UpdateStatus(ctx, job.ID, domain.MemoryJobFailed, err.Error()); updateErr != nil {
				return processed, updateErr
			}
			if w.Logger != nil {
				w.Logger.Warn("runtime: memory job failed", "job_id", job.ID, "error", err.Error())
			}
			processed++
			continue
		}

		if err := w.Repos.MemoryJobs.UpdateStatus(ctx, job.ID, domain.MemoryJobDone, ""); err != nil {
			return processed, err
		}
		processed++
	}

	return processed, nil
}

func (w *MemoryJobWorker) processJob(ctx context.Context, job domain.MemoryJob) error {
	switch job.Kind {
	case MemoryJobKindProviderWrite:
		return w.processProviderWriteJob(ctx, job)
	case MemoryJobKindMarkdownWrite:
		return w.processMarkdownWriteJob(ctx, job)
	case MemoryJobKindMarkdownPromote:
		return w.processMarkdownPromoteJob(ctx, job)
	case MemoryJobKindMarkdownReview:
		return fmt.Errorf("review job %s requires manual approval", job.ID)
	default:
		return fmt.Errorf("unsupported memory job kind %q", job.Kind)
	}
}

func (w *MemoryJobWorker) processProviderWriteJob(ctx context.Context, job domain.MemoryJob) error {
	if !w.Enabled() {
		return fmt.Errorf("memory provider unavailable")
	}

	var payload providerWriteJobPayload
	if err := json.Unmarshal([]byte(job.PayloadJSON), &payload); err != nil {
		return fmt.Errorf("decode provider write payload: %w", err)
	}
	if payload.Scope.Kind == "" {
		return fmt.Errorf("provider write payload missing scope")
	}
	if len(payload.Records) == 0 {
		return fmt.Errorf("provider write payload missing records")
	}

	writeTimeout := w.ProcessTimeout
	if writeTimeout <= 0 {
		writeTimeout = memoryWriteTimeout
	}
	writeCtx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return w.Memory.Write(writeCtx, payload.Scope, payload.Records)
}

func (w *MemoryJobWorker) processMarkdownWriteJob(ctx context.Context, job domain.MemoryJob) error {
	if w.Workspace == nil || !w.Workspace.Enabled() {
		return fmt.Errorf("workspace markdown memory is unavailable")
	}

	var payload markdownWriteJobPayload
	if err := json.Unmarshal([]byte(job.PayloadJSON), &payload); err != nil {
		return fmt.Errorf("decode markdown write payload: %w", err)
	}
	if payload.Scope.Kind == "" {
		return fmt.Errorf("markdown write payload missing scope")
	}
	if len(payload.Records) == 0 {
		return fmt.Errorf("markdown write payload missing records")
	}

	return w.Workspace.AppendJournal(ctx, workspacectx.JournalWriteInput{
		ProfileID:        payload.Scope.ProfileID,
		PersonID:         payload.Scope.PersonID,
		RoomID:           payload.Scope.RoomID,
		ConversationSlug: payload.ConversationSlug,
		Scope:            payload.Scope.Kind,
		Records:          payload.Records,
	})
}

func (w *MemoryJobWorker) processMarkdownPromoteJob(ctx context.Context, job domain.MemoryJob) error {
	if w.Workspace == nil || !w.Workspace.Enabled() {
		return fmt.Errorf("workspace markdown memory is unavailable")
	}

	var payload DurablePromoteJobPayload
	if err := json.Unmarshal([]byte(job.PayloadJSON), &payload); err != nil {
		return fmt.Errorf("decode markdown promote payload: %w", err)
	}
	if payload.Scope.Kind == "" {
		return fmt.Errorf("markdown promote payload missing scope")
	}
	if len(payload.Facts) == 0 {
		return fmt.Errorf("markdown promote payload missing facts")
	}

	_, err := w.Workspace.AppendDurable(ctx, workspacectx.DurableWriteInput{
		ProfileID:        payload.Scope.ProfileID,
		PersonID:         payload.Scope.PersonID,
		RoomID:           payload.Scope.RoomID,
		ConversationSlug: payload.ConversationSlug,
		Scope:            payload.Scope.Kind,
		Facts:            append([]string(nil), payload.Facts...),
		UpdatedBy:        payload.UpdatedBy,
	})
	return err
}

func BuildDurablePromotionMemoryJob(
	now time.Time,
	sourceMessageID domain.MessageID,
	input workspacectx.DurableWriteInput,
) (domain.MemoryJob, error) {
	scope := memory.Scope{
		Kind:           input.Scope,
		ProfileID:      input.ProfileID,
		RoomID:         input.RoomID,
		ConversationID: input.ConversationID,
		PersonID:       input.PersonID,
	}
	if scope.Kind == "" {
		return domain.MemoryJob{}, fmt.Errorf("durable promotion requires scope")
	}
	if len(input.Facts) == 0 {
		return domain.MemoryJob{}, fmt.Errorf("durable promotion requires facts")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}

	payloadJSON, err := json.Marshal(DurablePromoteJobPayload{
		Scope:            scope,
		ConversationSlug: input.ConversationSlug,
		Facts:            append([]string(nil), input.Facts...),
		UpdatedBy:        input.UpdatedBy,
	})
	if err != nil {
		return domain.MemoryJob{}, fmt.Errorf("encode durable promotion payload: %w", err)
	}
	targetType, targetID := memoryJobTarget(scope)
	return domain.MemoryJob{
		ID:              buildQueuedMemoryJobID(MemoryJobKindMarkdownPromote, sourceMessageID, scope.Kind, now),
		Kind:            MemoryJobKindMarkdownPromote,
		TargetType:      targetType,
		TargetID:        targetID,
		SourceMessageID: sourceMessageID,
		PayloadJSON:     string(payloadJSON),
		Status:          domain.MemoryJobPending,
		CreatedAt:       now,
		UpdatedAt:       now,
	}, nil
}

func BuildDurableReviewMemoryJob(
	now time.Time,
	sourceMessageID domain.MessageID,
	input workspacectx.DurableWriteInput,
	conflicts []workspacectx.DurableConflict,
) (domain.MemoryJob, error) {
	scope := memory.Scope{
		Kind:           input.Scope,
		ProfileID:      input.ProfileID,
		RoomID:         input.RoomID,
		ConversationID: input.ConversationID,
		PersonID:       input.PersonID,
	}
	if scope.Kind == "" {
		return domain.MemoryJob{}, fmt.Errorf("durable review requires scope")
	}
	if len(input.Facts) == 0 {
		return domain.MemoryJob{}, fmt.Errorf("durable review requires facts")
	}
	if len(conflicts) == 0 {
		return domain.MemoryJob{}, fmt.Errorf("durable review requires conflicts")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}

	payloadJSON, err := json.Marshal(DurableReviewJobPayload{
		Scope:            scope,
		ConversationSlug: input.ConversationSlug,
		Facts:            append([]string(nil), input.Facts...),
		UpdatedBy:        input.UpdatedBy,
		Conflicts:        append([]workspacectx.DurableConflict(nil), conflicts...),
	})
	if err != nil {
		return domain.MemoryJob{}, fmt.Errorf("encode durable review payload: %w", err)
	}
	targetType, targetID := memoryJobTarget(scope)
	return domain.MemoryJob{
		ID:              buildQueuedMemoryJobID(MemoryJobKindMarkdownReview, sourceMessageID, scope.Kind, now),
		Kind:            MemoryJobKindMarkdownReview,
		TargetType:      targetType,
		TargetID:        targetID,
		SourceMessageID: sourceMessageID,
		PayloadJSON:     string(payloadJSON),
		Status:          domain.MemoryJobReview,
		CreatedAt:       now,
		UpdatedAt:       now,
	}, nil
}

func buildProviderMemoryJobs(
	now time.Time,
	roomContext RoomContext,
	promptPackage PromptPackage,
	replyMessage domain.Message,
) ([]domain.MemoryJob, error) {
	settings := normalizeConversationSettings(roomContext.ConversationSettings, roomContext.Conversation.ID)
	if !settings.Enabled || !settings.AllowProviderMemory || !providerJournalEnabled(settings) {
		return nil, nil
	}

	writes := buildMemoryScopeWrites(roomContext, promptPackage, replyMessage, false)
	if len(writes) == 0 {
		return nil, nil
	}

	jobs := make([]domain.MemoryJob, 0, len(writes))
	for _, write := range writes {
		payloadJSON, err := json.Marshal(providerWriteJobPayload{
			Scope:   write.scope,
			Records: write.records,
		})
		if err != nil {
			return nil, fmt.Errorf("encode provider memory job payload: %w", err)
		}
		targetType, targetID := memoryJobTarget(write.scope)
		jobs = append(jobs, domain.MemoryJob{
			ID:              buildMemoryJobID(MemoryJobKindProviderWrite, replyMessage.ID, write.scope.Kind),
			Kind:            MemoryJobKindProviderWrite,
			TargetType:      targetType,
			TargetID:        targetID,
			SourceMessageID: replyMessage.ID,
			PayloadJSON:     string(payloadJSON),
			Status:          domain.MemoryJobPending,
			CreatedAt:       now,
			UpdatedAt:       now,
		})
	}

	return jobs, nil
}

func buildMarkdownMemoryJobs(
	now time.Time,
	workspace *workspacectx.Loader,
	roomContext RoomContext,
	promptPackage PromptPackage,
	replyMessage domain.Message,
) ([]domain.MemoryJob, error) {
	settings := normalizeConversationSettings(roomContext.ConversationSettings, roomContext.Conversation.ID)
	if workspace == nil || !workspace.Enabled() ||
		!settings.Enabled || !settings.AllowMarkdownMemory || !markdownJournalEnabled(settings) {
		return nil, nil
	}

	writes := buildMemoryScopeWrites(roomContext, promptPackage, replyMessage, true)
	if len(writes) == 0 {
		return nil, nil
	}

	jobs := make([]domain.MemoryJob, 0, len(writes))
	for _, write := range writes {
		payloadJSON, err := json.Marshal(markdownWriteJobPayload{
			Scope:            write.scope,
			ConversationSlug: roomContext.Conversation.Slug,
			Records:          write.records,
		})
		if err != nil {
			return nil, fmt.Errorf("encode markdown memory job payload: %w", err)
		}
		targetType, targetID := memoryJobTarget(write.scope)
		jobs = append(jobs, domain.MemoryJob{
			ID:              buildMemoryJobID(MemoryJobKindMarkdownWrite, replyMessage.ID, write.scope.Kind),
			Kind:            MemoryJobKindMarkdownWrite,
			TargetType:      targetType,
			TargetID:        targetID,
			SourceMessageID: replyMessage.ID,
			PayloadJSON:     string(payloadJSON),
			Status:          domain.MemoryJobPending,
			CreatedAt:       now,
			UpdatedAt:       now,
		})
	}

	return jobs, nil
}

func enqueueMemoryJobsBestEffort(
	logger *slog.Logger,
	repos sqlitestore.Repositories,
	workspace *workspacectx.Loader,
	now func() time.Time,
	roomContext RoomContext,
	promptPackage PromptPackage,
	replyMessage domain.Message,
) {
	providerJobs, err := buildProviderMemoryJobs(now().UTC(), roomContext, promptPackage, replyMessage)
	if err != nil {
		if logger != nil {
			logger.Warn("runtime: failed to build provider memory jobs", "error", err.Error())
		}
		return
	}
	markdownJobs, err := buildMarkdownMemoryJobs(now().UTC(), workspace, roomContext, promptPackage, replyMessage)
	if err != nil {
		if logger != nil {
			logger.Warn("runtime: failed to build markdown memory jobs", "error", err.Error())
		}
		return
	}
	jobs := append(providerJobs, markdownJobs...)
	if len(jobs) == 0 {
		return
	}

	queueCtx, cancel := context.WithTimeout(context.Background(), memoryQueueTimeout)
	defer cancel()

	for _, job := range jobs {
		if err := repos.MemoryJobs.Insert(queueCtx, job); err != nil {
			if logger != nil {
				logger.Warn("runtime: failed to enqueue memory job", "job_id", job.ID, "error", err.Error())
			}
		}
	}
}

func providerJournalEnabled(settings domain.ConversationSettings) bool {
	switch settings.JournalMode {
	case domain.ConversationJournalOff, domain.ConversationJournalMarkdownOnly:
		return false
	default:
		return true
	}
}

func markdownJournalEnabled(settings domain.ConversationSettings) bool {
	switch settings.JournalMode {
	case domain.ConversationJournalOff, domain.ConversationJournalProviderOnly:
		return false
	default:
		return true
	}
}

func buildMemoryScopeWrites(
	roomContext RoomContext,
	promptPackage PromptPackage,
	replyMessage domain.Message,
	markdownOnly bool,
) []scopeWrite {
	settings := normalizeConversationSettings(roomContext.ConversationSettings, roomContext.Conversation.ID)
	userText := lastPromptContentByRole(promptPackage.Messages, domain.MessageRoleUser)
	if strings.TrimSpace(userText) == "" || strings.TrimSpace(replyMessage.ContentText) == "" {
		return nil
	}

	userRecord := memory.WriteRecord{
		ID:      fmt.Sprintf("mem:user:%s:%d", roomContext.Person.ID, roomContext.Session.LastMessageAt.UnixNano()),
		Content: userText,
		Metadata: map[string]string{
			"role": "user",
		},
		CreatedAt: roomContext.Session.LastMessageAt,
	}
	assistantRecord := memory.WriteRecord{
		ID:      fmt.Sprintf("mem:assistant:%s:%d", roomContext.Profile.ID, replyMessage.CreatedAt.UnixNano()),
		Content: replyMessage.ContentText,
		Metadata: map[string]string{
			"role": "assistant",
		},
		CreatedAt: replyMessage.CreatedAt,
	}

	writes := make([]scopeWrite, 0, 4)
	appendWrite := func(scope memory.Scope, records []memory.WriteRecord) {
		if len(records) == 0 {
			return
		}
		writes = append(writes, scopeWrite{
			scope:   scope,
			records: records,
		})
	}

	baseScope := memory.Scope{
		ProfileID:      roomContext.Profile.ID,
		RoomID:         roomContext.Room.ID,
		ConversationID: roomContext.Conversation.ID,
		PersonID:       roomContext.Person.ID,
	}

	if roomContext.Conversation.ID != "" && conversationWriteAllows(settings, memory.ScopeConversation) {
		scope := baseScope
		scope.Kind = memory.ScopeConversation
		appendWrite(scope, []memory.WriteRecord{userRecord, assistantRecord})
	}
	if conversationWriteAllows(settings, memory.ScopeRoom) {
		scope := baseScope
		scope.Kind = memory.ScopeRoom
		appendWrite(scope, []memory.WriteRecord{userRecord, assistantRecord})
	}
	if !markdownOnly && conversationWriteAllows(settings, memory.ScopePersonSummary) {
		scope := baseScope
		scope.Kind = memory.ScopePersonSummary
		appendWrite(scope, []memory.WriteRecord{userRecord})
	}
	if conversationWriteAllows(settings, memory.ScopePersonPrivate) {
		scope := baseScope
		scope.Kind = memory.ScopePersonPrivate
		privateRecords := []memory.WriteRecord{userRecord}
		if settings.WriteAssistantToPersonPrivate {
			privateRecords = append(privateRecords, assistantRecord)
		}
		appendWrite(scope, privateRecords)
	}

	return writes
}

func buildMemoryJobID(kind string, sourceMessageID domain.MessageID, scope memory.ScopeKind) string {
	return fmt.Sprintf("memjob:%s:%s:%s", kind, sourceMessageID, scope)
}

func buildQueuedMemoryJobID(
	kind string,
	sourceMessageID domain.MessageID,
	scope memory.ScopeKind,
	now time.Time,
) string {
	return fmt.Sprintf("memjob:%s:%s:%s:%d", kind, sourceMessageID, scope, now.UTC().UnixNano())
}

func memoryJobTarget(scope memory.Scope) (string, string) {
	switch scope.Kind {
	case memory.ScopeConversation:
		return "conversation", string(scope.ConversationID)
	case memory.ScopeRoom:
		return "room", string(scope.RoomID)
	case memory.ScopePersonSummary:
		return "person_summary", string(scope.PersonID)
	case memory.ScopePersonPrivate:
		return "person_private", string(scope.PersonID)
	default:
		return string(scope.Kind), ""
	}
}
