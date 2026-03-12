package runtime

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"time"

	"goclaw/internal/config"
	"goclaw/internal/domain"
	"goclaw/internal/memory"
	sqlitestore "goclaw/internal/store/sqlite"
	workspacectx "goclaw/internal/workspace"
)

const (
	defaultAutoPromoteRecentMessagesLimit = 128
	defaultAutoPromoteMinimumEvidence     = 2
	defaultAutoPromotePollInterval        = 30 * time.Second
	autoPromoteUpdatedBy                  = "auto:memory_compaction"
	autoPromoteJobKind                    = "markdown_promote_auto"
)

type autoPromoteCandidate struct {
	scope memory.ScopeKind
	facts []string
}

type autoPromoteContext struct {
	profileID        domain.ProfileID
	personID         domain.PersonID
	roomID           domain.RoomID
	conversationID   domain.ConversationID
	conversationSlug string
	latestMessage    domain.Message
}

type autoPromoteStrongBucket struct {
	key              string
	scope            memory.ScopeKind
	fact             string
	profileID        domain.ProfileID
	personID         domain.PersonID
	roomID           domain.RoomID
	conversationID   domain.ConversationID
	conversationSlug string
	latestMessage    domain.Message
	evidence         int
}

type MemoryCompactionWorker struct {
	Repos        sqlitestore.Repositories
	Workspace    *workspacectx.Loader
	Logger       *slog.Logger
	Config       config.AutoPromoteConfig
	PollInterval time.Duration
}

func NewMemoryCompactionWorker(
	repos sqlitestore.Repositories,
	workspace *workspacectx.Loader,
	cfg config.AutoPromoteConfig,
	logger *slog.Logger,
) *MemoryCompactionWorker {
	return &MemoryCompactionWorker{
		Repos:        repos,
		Workspace:    workspace,
		Logger:       logger,
		Config:       cfg,
		PollInterval: autoPromotePollInterval(cfg),
	}
}

func (w *MemoryCompactionWorker) Enabled() bool {
	return w != nil &&
		w.Config.Enabled &&
		w.Workspace != nil &&
		w.Workspace.Enabled()
}

func (w *MemoryCompactionWorker) Name() string {
	return "memory-compaction"
}

func (w *MemoryCompactionWorker) Run(ctx context.Context) error {
	if !w.Enabled() {
		return nil
	}
	if _, err := w.DrainOnce(ctx); err != nil {
		return err
	}

	interval := w.PollInterval
	if interval <= 0 {
		interval = defaultAutoPromotePollInterval
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

func (w *MemoryCompactionWorker) DrainOnce(ctx context.Context) (int, error) {
	if !w.Enabled() {
		return 0, nil
	}

	messages, err := w.Repos.Messages.ListRecent(ctx, autoPromoteRecentLimit(w.Config))
	if err != nil {
		return 0, err
	}

	enqueued := 0
	for _, message := range messages {
		count, err := w.processMessage(ctx, message)
		if err != nil {
			return enqueued, err
		}
		enqueued += count
	}
	if w.Config.StrongPatternsEnabled {
		count, err := w.processStrongPatterns(ctx, messages)
		if err != nil {
			return enqueued, err
		}
		enqueued += count
	}
	return enqueued, nil
}

func (w *MemoryCompactionWorker) processMessage(ctx context.Context, message domain.Message) (int, error) {
	if message.Role != domain.MessageRoleUser || strings.TrimSpace(message.ContentText) == "" {
		return 0, nil
	}
	if message.ConversationID == "" {
		return 0, nil
	}

	candidates := extractAutoPromoteCandidates(message.ContentText, w.Config)
	if len(candidates) == 0 {
		return 0, nil
	}

	settings, err := w.Repos.ConversationSettings.Resolve(ctx, message.ConversationID)
	if err != nil {
		return 0, err
	}
	settings = normalizeConversationSettings(settings, message.ConversationID)
	if !settings.Enabled || !settings.AllowMarkdownMemory {
		return 0, nil
	}

	conversationSlug := ""
	if message.ConversationID != "" {
		conversation, err := w.Repos.Conversations.Get(ctx, message.ConversationID)
		if err != nil {
			return 0, err
		}
		conversationSlug = conversation.Slug
	}

	enqueued := 0
	for _, candidate := range candidates {
		if !w.scopeEnabled(candidate.scope) || !conversationWriteAllows(settings, candidate.scope) {
			continue
		}
		if candidate.scope == memory.ScopeConversation && strings.TrimSpace(conversationSlug) == "" {
			continue
		}

		input := workspacectx.DurableWriteInput{
			ProfileID:        message.ProfileID,
			PersonID:         message.PersonID,
			RoomID:           message.RoomID,
			ConversationID:   message.ConversationID,
			ConversationSlug: conversationSlug,
			Scope:            candidate.scope,
			Facts:            append([]string(nil), candidate.facts...),
			UpdatedBy:        autoPromoteUpdatedBy,
			Timestamp:        message.CreatedAt,
		}
		inserted, err := w.enqueueAutoPromoteJob(
			ctx,
			autoPromoteSignature(
				string(message.ConversationID),
				string(candidate.scope),
				strings.Join(candidate.facts, "\n"),
				string(message.ID),
			),
			message,
			input,
		)
		if err != nil {
			return enqueued, err
		}
		if inserted {
			enqueued++
		}
	}

	return enqueued, nil
}

func (w *MemoryCompactionWorker) processStrongPatterns(
	ctx context.Context,
	messages []domain.Message,
) (int, error) {
	buckets, err := w.collectStrongPatternBuckets(ctx, messages)
	if err != nil {
		return 0, err
	}

	threshold := autoPromoteMinimumEvidence(w.Config)
	enqueued := 0
	for _, bucket := range buckets {
		if bucket.scope == memory.ScopePersonPrivate || bucket.evidence < threshold {
			continue
		}
		if !w.scopeEnabled(bucket.scope) {
			continue
		}

		settings, err := w.Repos.ConversationSettings.Resolve(ctx, bucket.conversationID)
		if err != nil {
			return enqueued, err
		}
		settings = normalizeConversationSettings(settings, bucket.conversationID)
		if !settings.Enabled || !settings.AllowMarkdownMemory {
			continue
		}
		if bucket.scope == memory.ScopeConversation && strings.TrimSpace(bucket.conversationSlug) == "" {
			continue
		}
		if !conversationWriteAllows(settings, bucket.scope) {
			continue
		}

		input := workspacectx.DurableWriteInput{
			ProfileID:        bucket.profileID,
			PersonID:         bucket.personID,
			RoomID:           bucket.roomID,
			ConversationID:   bucket.conversationID,
			ConversationSlug: bucket.conversationSlug,
			Scope:            bucket.scope,
			Facts:            []string{bucket.fact},
			UpdatedBy:        autoPromoteUpdatedBy,
			Timestamp:        bucket.latestMessage.CreatedAt,
		}
		inserted, err := w.enqueueAutoPromoteJob(ctx, bucket.key, bucket.latestMessage, input)
		if err != nil {
			return enqueued, err
		}
		if inserted {
			enqueued++
		}
	}
	return enqueued, nil
}

func (w *MemoryCompactionWorker) enqueueAutoPromoteJob(
	ctx context.Context,
	signature string,
	sourceMessage domain.Message,
	input workspacectx.DurableWriteInput,
) (bool, error) {
	job, err := w.buildAutoPromoteJob(ctx, signature, sourceMessage, input)
	if err != nil {
		return false, err
	}
	return w.Repos.MemoryJobs.InsertOrIgnore(ctx, job)
}

func (w *MemoryCompactionWorker) buildAutoPromoteJob(
	ctx context.Context,
	signature string,
	sourceMessage domain.Message,
	input workspacectx.DurableWriteInput,
) (domain.MemoryJob, error) {
	if w.Workspace != nil && w.Workspace.Enabled() {
		conflictResult, err := w.Workspace.CheckDurableConflicts(ctx, input)
		if err != nil {
			return domain.MemoryJob{}, err
		}
		if len(conflictResult.Conflicts) > 0 {
			job, err := BuildDurableReviewMemoryJob(
				sourceMessage.CreatedAt,
				sourceMessage.ID,
				input,
				conflictResult.Conflicts,
			)
			if err != nil {
				return domain.MemoryJob{}, err
			}
			job.ID = fmt.Sprintf(
				"memjob:%s:%s:%s",
				autoPromoteJobKind,
				MemoryJobKindMarkdownReview,
				autoPromoteSignature(signature),
			)
			return job, nil
		}
	}
	return buildAutomaticDurablePromotionMemoryJob(signature, sourceMessage, input)
}

func (w *MemoryCompactionWorker) collectStrongPatternBuckets(
	ctx context.Context,
	messages []domain.Message,
) ([]autoPromoteStrongBucket, error) {
	contexts, transcriptSets, err := w.collectAutoPromoteContexts(ctx, messages)
	if err != nil {
		return nil, err
	}
	if len(contexts) == 0 {
		return nil, nil
	}

	buckets := make(map[string]*autoPromoteStrongBucket)
	if w.Config.Sources.Transcript {
		for _, message := range messages {
			if message.Role != domain.MessageRoleUser || strings.TrimSpace(message.ContentText) == "" {
				continue
			}
			contextKey := autoPromoteContextKey(message)
			ctxInfo, ok := contexts[contextKey]
			if !ok {
				continue
			}
			for _, candidate := range extractStrongPatternAutoPromoteCandidates(message.ContentText) {
				w.addStrongPatternEvidence(
					buckets,
					ctxInfo,
					candidate.scope,
					candidate.facts,
				)
			}
		}
	}

	if w.Config.Sources.Journals && w.Workspace != nil && w.Workspace.Enabled() {
		for contextKey, ctxInfo := range contexts {
			entries, err := w.Workspace.LoadJournalEntries(ctx, workspacectx.JournalLoadInput{
				PersonID:         ctxInfo.personID,
				RoomID:           ctxInfo.roomID,
				ConversationSlug: ctxInfo.conversationSlug,
			})
			if err != nil {
				return nil, err
			}
			for _, entry := range entries {
				role := strings.ToLower(strings.TrimSpace(entry.Role))
				if role != "user" && role != "note" {
					continue
				}
				normalizedContent := normalizeCompactionFact(entry.Content)
				if normalizedContent == "" {
					continue
				}
				if _, exists := transcriptSets[contextKey][normalizedContent]; exists {
					continue
				}
				for _, candidate := range extractStrongPatternAutoPromoteCandidates(entry.Content) {
					if !strongPatternJournalScopeAllowed(entry.Scope, candidate.scope) {
						continue
					}
					w.addStrongPatternEvidence(
						buckets,
						ctxInfo,
						candidate.scope,
						candidate.facts,
					)
				}
			}
		}
	}

	out := make([]autoPromoteStrongBucket, 0, len(buckets))
	for _, bucket := range buckets {
		if bucket.evidence < thresholdForStrongBucket(w.Config, bucket.scope) {
			continue
		}
		out = append(out, *bucket)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].evidence == out[j].evidence {
			if out[i].scope == out[j].scope {
				return out[i].fact < out[j].fact
			}
			return out[i].scope < out[j].scope
		}
		return out[i].evidence > out[j].evidence
	})
	return out, nil
}

func (w *MemoryCompactionWorker) collectAutoPromoteContexts(
	ctx context.Context,
	messages []domain.Message,
) (map[string]autoPromoteContext, map[string]map[string]struct{}, error) {
	contexts := make(map[string]autoPromoteContext)
	transcriptSets := make(map[string]map[string]struct{})
	conversationSlugs := make(map[domain.ConversationID]string)

	for _, message := range messages {
		if message.Role != domain.MessageRoleUser || strings.TrimSpace(message.ContentText) == "" {
			continue
		}
		if message.ConversationID == "" {
			continue
		}
		contextKey := autoPromoteContextKey(message)
		if _, ok := transcriptSets[contextKey]; !ok {
			transcriptSets[contextKey] = make(map[string]struct{})
		}
		normalizedContent := normalizeCompactionFact(message.ContentText)
		if normalizedContent != "" {
			transcriptSets[contextKey][normalizedContent] = struct{}{}
		}
		existing, ok := contexts[contextKey]
		if ok && !message.CreatedAt.After(existing.latestMessage.CreatedAt) {
			continue
		}
		contexts[contextKey] = autoPromoteContext{
			profileID:      message.ProfileID,
			personID:       message.PersonID,
			roomID:         message.RoomID,
			conversationID: message.ConversationID,
			latestMessage:  message,
		}
		conversationSlugs[message.ConversationID] = ""
	}

	for conversationID := range conversationSlugs {
		conversation, err := w.Repos.Conversations.Get(ctx, conversationID)
		if err != nil {
			return nil, nil, err
		}
		conversationSlugs[conversationID] = conversation.Slug
	}

	for key, ctxInfo := range contexts {
		ctxInfo.conversationSlug = conversationSlugs[ctxInfo.conversationID]
		contexts[key] = ctxInfo
	}
	return contexts, transcriptSets, nil
}

func (w *MemoryCompactionWorker) addStrongPatternEvidence(
	buckets map[string]*autoPromoteStrongBucket,
	ctxInfo autoPromoteContext,
	scope memory.ScopeKind,
	facts []string,
) {
	if scope == memory.ScopePersonPrivate {
		return
	}
	for _, fact := range dedupeFacts(facts) {
		targetKey := strongPatternTargetKey(scope, ctxInfo, fact)
		bucket, ok := buckets[targetKey]
		if !ok {
			bucket = &autoPromoteStrongBucket{
				key:              targetKey,
				scope:            scope,
				fact:             fact,
				profileID:        ctxInfo.profileID,
				personID:         ctxInfo.personID,
				roomID:           ctxInfo.roomID,
				conversationID:   ctxInfo.conversationID,
				conversationSlug: ctxInfo.conversationSlug,
				latestMessage:    ctxInfo.latestMessage,
			}
			buckets[targetKey] = bucket
		}
		bucket.evidence++
		if ctxInfo.latestMessage.CreatedAt.After(bucket.latestMessage.CreatedAt) {
			bucket.latestMessage = ctxInfo.latestMessage
			bucket.conversationID = ctxInfo.conversationID
			bucket.conversationSlug = ctxInfo.conversationSlug
		}
	}
}

func strongPatternTargetKey(
	scope memory.ScopeKind,
	ctxInfo autoPromoteContext,
	fact string,
) string {
	var target string
	switch scope {
	case memory.ScopeConversation:
		target = strings.Join([]string{
			string(ctxInfo.profileID),
			string(ctxInfo.roomID),
			string(ctxInfo.conversationID),
		}, "|")
	case memory.ScopeRoom:
		target = strings.Join([]string{
			string(ctxInfo.profileID),
			string(ctxInfo.roomID),
		}, "|")
	case memory.ScopePersonSummary:
		target = strings.Join([]string{
			string(ctxInfo.profileID),
			string(ctxInfo.personID),
		}, "|")
	default:
		target = strings.Join([]string{
			string(ctxInfo.profileID),
			string(ctxInfo.roomID),
			string(ctxInfo.personID),
			string(ctxInfo.conversationID),
		}, "|")
	}
	return autoPromoteSignature("strong", string(scope), target, fact)
}

func strongPatternJournalScopeAllowed(
	journalScope memory.ScopeKind,
	candidateScope memory.ScopeKind,
) bool {
	switch journalScope {
	case memory.ScopeConversation:
		return candidateScope == memory.ScopeConversation
	case memory.ScopeRoom:
		return candidateScope == memory.ScopeRoom
	case memory.ScopePersonPrivate:
		return candidateScope == memory.ScopePersonSummary
	default:
		return false
	}
}

func autoPromoteContextKey(message domain.Message) string {
	return strings.Join([]string{
		string(message.ProfileID),
		string(message.RoomID),
		string(message.PersonID),
		string(message.ConversationID),
	}, "|")
}

func (w *MemoryCompactionWorker) scopeEnabled(scope memory.ScopeKind) bool {
	switch scope {
	case memory.ScopeConversation:
		return w.Config.Layers.Conversation
	case memory.ScopeRoom:
		return w.Config.Layers.Room
	case memory.ScopePersonSummary:
		return w.Config.Layers.PersonSummary
	case memory.ScopePersonPrivate:
		return w.Config.Layers.PersonPrivate
	default:
		return false
	}
}

func autoPromoteRecentLimit(cfg config.AutoPromoteConfig) int {
	if cfg.RecentMessagesLimit > 0 {
		return cfg.RecentMessagesLimit
	}
	return defaultAutoPromoteRecentMessagesLimit
}

func autoPromoteMinimumEvidence(cfg config.AutoPromoteConfig) int {
	if cfg.MinimumEvidence > 0 {
		return cfg.MinimumEvidence
	}
	return defaultAutoPromoteMinimumEvidence
}

func autoPromotePollInterval(cfg config.AutoPromoteConfig) time.Duration {
	if cfg.PollIntervalSeconds > 0 {
		return time.Duration(cfg.PollIntervalSeconds) * time.Second
	}
	return defaultAutoPromotePollInterval
}

func buildAutomaticDurablePromotionMemoryJob(
	signature string,
	sourceMessage domain.Message,
	input workspacectx.DurableWriteInput,
) (domain.MemoryJob, error) {
	job, err := BuildDurablePromotionMemoryJob(sourceMessage.CreatedAt, sourceMessage.ID, input)
	if err != nil {
		return domain.MemoryJob{}, err
	}
	job.ID = fmt.Sprintf(
		"memjob:%s:%s:%s",
		autoPromoteJobKind,
		input.Scope,
		autoPromoteSignature(signature),
	)
	return job, nil
}

func autoPromoteSignature(parts ...string) string {
	sum := sha1.Sum([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:8])
}

func thresholdForStrongBucket(
	cfg config.AutoPromoteConfig,
	scope memory.ScopeKind,
) int {
	if scope == memory.ScopePersonPrivate {
		return 1 << 30
	}
	return autoPromoteMinimumEvidence(cfg)
}

func extractAutoPromoteCandidates(content string, cfg config.AutoPromoteConfig) []autoPromoteCandidate {
	candidates := extractExplicitAutoPromoteCandidates(content)
	if cfg.WeakPatternsEnabled {
		candidates = append(candidates, extractWeakPatternAutoPromoteCandidates(content)...)
	}
	return dedupeAutoPromoteCandidates(candidates)
}

func extractExplicitAutoPromoteCandidates(content string) []autoPromoteCandidate {
	type markerDef struct {
		prefix string
		scope  memory.ScopeKind
	}
	markers := []markerDef{
		{prefix: "remember conversation:", scope: memory.ScopeConversation},
		{prefix: "remember room:", scope: memory.ScopeRoom},
		{prefix: "remember summary:", scope: memory.ScopePersonSummary},
		{prefix: "remember private:", scope: memory.ScopePersonPrivate},
		{prefix: "记住会话:", scope: memory.ScopeConversation},
		{prefix: "记住群:", scope: memory.ScopeRoom},
		{prefix: "记住群聊:", scope: memory.ScopeRoom},
		{prefix: "记住房间:", scope: memory.ScopeRoom},
		{prefix: "记住摘要:", scope: memory.ScopePersonSummary},
		{prefix: "记住私有:", scope: memory.ScopePersonPrivate},
	}

	lines := strings.Split(strings.ReplaceAll(content, "：", ":"), "\n")
	type openBlock struct {
		scope memory.ScopeKind
		facts []string
	}
	var block openBlock
	var out []autoPromoteCandidate

	flush := func() {
		if block.scope == "" || len(block.facts) == 0 {
			block = openBlock{}
			return
		}
		facts := dedupeFacts(block.facts)
		if len(facts) > 0 {
			out = append(out, autoPromoteCandidate{scope: block.scope, facts: facts})
		}
		block = openBlock{}
	}

	for _, rawLine := range lines {
		trimmed := strings.TrimSpace(rawLine)
		lower := strings.ToLower(trimmed)

		matched := false
		for _, marker := range markers {
			prefix := marker.prefix
			check := trimmed
			if strings.HasPrefix(prefix, "remember") {
				check = lower
			}
			if !strings.HasPrefix(check, prefix) {
				continue
			}
			flush()
			rest := strings.TrimSpace(trimmed[len(prefix):])
			block.scope = marker.scope
			if rest != "" {
				block.facts = append(block.facts, rest)
			}
			matched = true
			break
		}
		if matched {
			continue
		}

		if block.scope == "" {
			continue
		}
		if trimmed == "" {
			flush()
			continue
		}

		fact := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(trimmed, "-"), "*"))
		if fact != "" {
			block.facts = append(block.facts, fact)
		}
	}

	flush()
	return out
}

func extractWeakPatternAutoPromoteCandidates(content string) []autoPromoteCandidate {
	normalized := strings.ReplaceAll(content, "：", ":")
	lines := strings.Split(normalized, "\n")
	var out []autoPromoteCandidate
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		switch {
		case strings.HasPrefix(line, "我偏好"):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopePersonSummary,
				facts: []string{"User preference: " + strings.TrimSpace(strings.TrimPrefix(line, "我偏好"))},
			})
		case strings.HasPrefix(line, "我喜欢"):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopePersonSummary,
				facts: []string{"User preference: " + strings.TrimSpace(strings.TrimPrefix(line, "我喜欢"))},
			})
		case strings.HasPrefix(line, "请叫我"):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopePersonSummary,
				facts: []string{"Preferred form of address: " + strings.TrimSpace(strings.TrimPrefix(line, "请叫我"))},
			})
		case strings.HasPrefix(line, "以后叫我"):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopePersonSummary,
				facts: []string{"Preferred form of address: " + strings.TrimSpace(strings.TrimPrefix(line, "以后叫我"))},
			})
		case strings.HasPrefix(line, "这个群主要是"):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopeRoom,
				facts: []string{"Room purpose: " + strings.TrimSpace(strings.TrimPrefix(line, "这个群主要是"))},
			})
		case strings.HasPrefix(line, "这个群是用来"):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopeRoom,
				facts: []string{"Room purpose: " + strings.TrimSpace(strings.TrimPrefix(line, "这个群是用来"))},
			})
		case strings.HasPrefix(line, "这次会话主要是"):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopeConversation,
				facts: []string{"Conversation focus: " + strings.TrimSpace(strings.TrimPrefix(line, "这次会话主要是"))},
			})
		case strings.HasPrefix(line, "这条会话主要是"):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopeConversation,
				facts: []string{"Conversation focus: " + strings.TrimSpace(strings.TrimPrefix(line, "这条会话主要是"))},
			})
		case strings.HasPrefix(lower, "i prefer "):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopePersonSummary,
				facts: []string{"User preference: " + strings.TrimSpace(line[len("I prefer "):])},
			})
		case strings.HasPrefix(lower, "please call me "):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopePersonSummary,
				facts: []string{"Preferred form of address: " + strings.TrimSpace(line[len("Please call me "):])},
			})
		case strings.HasPrefix(lower, "this room is for "):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopeRoom,
				facts: []string{"Room purpose: " + strings.TrimSpace(line[len("This room is for "):])},
			})
		case strings.HasPrefix(lower, "this group is for "):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopeRoom,
				facts: []string{"Room purpose: " + strings.TrimSpace(line[len("This group is for "):])},
			})
		case strings.HasPrefix(lower, "this conversation is about "):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopeConversation,
				facts: []string{"Conversation focus: " + strings.TrimSpace(line[len("This conversation is about "):])},
			})
		}
	}

	re := regexp.MustCompile(`\s+`)
	for i := range out {
		for j := range out[i].facts {
			out[i].facts[j] = re.ReplaceAllString(strings.TrimSpace(out[i].facts[j]), " ")
		}
	}
	return out
}

func extractStrongPatternAutoPromoteCandidates(content string) []autoPromoteCandidate {
	normalized := strings.ReplaceAll(content, "：", ":")
	lines := strings.Split(normalized, "\n")
	var out []autoPromoteCandidate
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		switch {
		case strings.HasPrefix(line, "我偏好"):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopePersonSummary,
				facts: []string{"User preference: " + strings.TrimSpace(strings.TrimPrefix(line, "我偏好"))},
			})
		case strings.HasPrefix(line, "我喜欢"):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopePersonSummary,
				facts: []string{"User preference: " + strings.TrimSpace(strings.TrimPrefix(line, "我喜欢"))},
			})
		case strings.HasPrefix(line, "我一般喜欢"):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopePersonSummary,
				facts: []string{"User preference: " + strings.TrimSpace(strings.TrimPrefix(line, "我一般喜欢"))},
			})
		case strings.HasPrefix(line, "我通常喜欢"):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopePersonSummary,
				facts: []string{"User preference: " + strings.TrimSpace(strings.TrimPrefix(line, "我通常喜欢"))},
			})
		case strings.HasPrefix(line, "我更喜欢"):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopePersonSummary,
				facts: []string{"User preference: " + strings.TrimSpace(strings.TrimPrefix(line, "我更喜欢"))},
			})
		case strings.HasPrefix(line, "请叫我"):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopePersonSummary,
				facts: []string{"Preferred form of address: " + strings.TrimSpace(strings.TrimPrefix(line, "请叫我"))},
			})
		case strings.HasPrefix(line, "以后叫我"):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopePersonSummary,
				facts: []string{"Preferred form of address: " + strings.TrimSpace(strings.TrimPrefix(line, "以后叫我"))},
			})
		case strings.HasPrefix(line, "以后都叫我"):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopePersonSummary,
				facts: []string{"Preferred form of address: " + strings.TrimSpace(strings.TrimPrefix(line, "以后都叫我"))},
			})
		case strings.HasPrefix(line, "默认叫我"):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopePersonSummary,
				facts: []string{"Preferred form of address: " + strings.TrimSpace(strings.TrimPrefix(line, "默认叫我"))},
			})
		case strings.HasPrefix(line, "回答尽量"):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopePersonSummary,
				facts: []string{"User preference: 回答尽量" + strings.TrimSpace(strings.TrimPrefix(line, "回答尽量"))},
			})
		case strings.HasPrefix(line, "回复尽量"):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopePersonSummary,
				facts: []string{"User preference: 回复尽量" + strings.TrimSpace(strings.TrimPrefix(line, "回复尽量"))},
			})
		case strings.HasPrefix(line, "这个群主要是"):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopeRoom,
				facts: []string{"Room purpose: " + strings.TrimSpace(strings.TrimPrefix(line, "这个群主要是"))},
			})
		case strings.HasPrefix(line, "这个群主要聊"):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopeRoom,
				facts: []string{"Room purpose: " + strings.TrimSpace(strings.TrimPrefix(line, "这个群主要聊"))},
			})
		case strings.HasPrefix(line, "这个群负责"):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopeRoom,
				facts: []string{"Room purpose: " + strings.TrimSpace(strings.TrimPrefix(line, "这个群负责"))},
			})
		case strings.HasPrefix(line, "这个群用于"):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopeRoom,
				facts: []string{"Room purpose: " + strings.TrimSpace(strings.TrimPrefix(line, "这个群用于"))},
			})
		case strings.HasPrefix(line, "这个群是用来"):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopeRoom,
				facts: []string{"Room purpose: " + strings.TrimSpace(strings.TrimPrefix(line, "这个群是用来"))},
			})
		case strings.HasPrefix(line, "本群主要"):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopeRoom,
				facts: []string{"Room purpose: " + strings.TrimSpace(strings.TrimPrefix(line, "本群主要"))},
			})
		case strings.HasPrefix(line, "这里主要聊"):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopeRoom,
				facts: []string{"Room purpose: " + strings.TrimSpace(strings.TrimPrefix(line, "这里主要聊"))},
			})
		case strings.HasPrefix(line, "这次会话主要是"):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopeConversation,
				facts: []string{"Conversation focus: " + strings.TrimSpace(strings.TrimPrefix(line, "这次会话主要是"))},
			})
		case strings.HasPrefix(line, "这条会话主要是"):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopeConversation,
				facts: []string{"Conversation focus: " + strings.TrimSpace(strings.TrimPrefix(line, "这条会话主要是"))},
			})
		case strings.HasPrefix(line, "本次会话主要是"):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopeConversation,
				facts: []string{"Conversation focus: " + strings.TrimSpace(strings.TrimPrefix(line, "本次会话主要是"))},
			})
		case strings.HasPrefix(line, "这次主要解决"):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopeConversation,
				facts: []string{"Conversation focus: " + strings.TrimSpace(strings.TrimPrefix(line, "这次主要解决"))},
			})
		case strings.HasPrefix(line, "这次先做"):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopeConversation,
				facts: []string{"Conversation focus: " + strings.TrimSpace(strings.TrimPrefix(line, "这次先做"))},
			})
		case strings.HasPrefix(line, "当前目标是"):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopeConversation,
				facts: []string{"Conversation focus: " + strings.TrimSpace(strings.TrimPrefix(line, "当前目标是"))},
			})
		case strings.HasPrefix(line, "本轮主要处理"):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopeConversation,
				facts: []string{"Conversation focus: " + strings.TrimSpace(strings.TrimPrefix(line, "本轮主要处理"))},
			})
		case strings.HasPrefix(lower, "i prefer "):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopePersonSummary,
				facts: []string{"User preference: " + strings.TrimSpace(line[len("I prefer "):])},
			})
		case strings.HasPrefix(lower, "i usually prefer "):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopePersonSummary,
				facts: []string{"User preference: " + strings.TrimSpace(line[len("I usually prefer "):])},
			})
		case strings.HasPrefix(lower, "i generally prefer "):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopePersonSummary,
				facts: []string{"User preference: " + strings.TrimSpace(line[len("I generally prefer "):])},
			})
		case strings.HasPrefix(lower, "please call me "):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopePersonSummary,
				facts: []string{"Preferred form of address: " + strings.TrimSpace(line[len("Please call me "):])},
			})
		case strings.HasPrefix(lower, "call me "):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopePersonSummary,
				facts: []string{"Preferred form of address: " + strings.TrimSpace(line[len("Call me "):])},
			})
		case strings.HasPrefix(lower, "default to "):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopePersonSummary,
				facts: []string{"User preference: default to " + strings.TrimSpace(line[len("Default to "):])},
			})
		case strings.HasPrefix(lower, "this room is for "):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopeRoom,
				facts: []string{"Room purpose: " + strings.TrimSpace(line[len("This room is for "):])},
			})
		case strings.HasPrefix(lower, "this room mainly handles "):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopeRoom,
				facts: []string{"Room purpose: " + strings.TrimSpace(line[len("This room mainly handles "):])},
			})
		case strings.HasPrefix(lower, "this room mainly discusses "):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopeRoom,
				facts: []string{"Room purpose: " + strings.TrimSpace(line[len("This room mainly discusses "):])},
			})
		case strings.HasPrefix(lower, "this group is for "):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopeRoom,
				facts: []string{"Room purpose: " + strings.TrimSpace(line[len("This group is for "):])},
			})
		case strings.HasPrefix(lower, "this conversation is about "):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopeConversation,
				facts: []string{"Conversation focus: " + strings.TrimSpace(line[len("This conversation is about "):])},
			})
		case strings.HasPrefix(lower, "current goal is "):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopeConversation,
				facts: []string{"Conversation focus: " + strings.TrimSpace(line[len("Current goal is "):])},
			})
		case strings.HasPrefix(lower, "this round focuses on "):
			out = append(out, autoPromoteCandidate{
				scope: memory.ScopeConversation,
				facts: []string{"Conversation focus: " + strings.TrimSpace(line[len("This round focuses on "):])},
			})
		}
	}

	re := regexp.MustCompile(`\s+`)
	for i := range out {
		for j := range out[i].facts {
			out[i].facts[j] = re.ReplaceAllString(strings.TrimSpace(out[i].facts[j]), " ")
		}
	}
	return dedupeAutoPromoteCandidates(out)
}

func dedupeAutoPromoteCandidates(candidates []autoPromoteCandidate) []autoPromoteCandidate {
	if len(candidates) == 0 {
		return nil
	}
	type bucket struct {
		seen  map[string]struct{}
		facts []string
	}
	byScope := make(map[memory.ScopeKind]*bucket)
	order := make([]memory.ScopeKind, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.scope == "" {
			continue
		}
		group, ok := byScope[candidate.scope]
		if !ok {
			group = &bucket{seen: make(map[string]struct{})}
			byScope[candidate.scope] = group
			order = append(order, candidate.scope)
		}
		for _, fact := range dedupeFacts(candidate.facts) {
			if _, exists := group.seen[fact]; exists {
				continue
			}
			group.seen[fact] = struct{}{}
			group.facts = append(group.facts, fact)
		}
	}

	out := make([]autoPromoteCandidate, 0, len(order))
	for _, scope := range order {
		group := byScope[scope]
		if len(group.facts) == 0 {
			continue
		}
		out = append(out, autoPromoteCandidate{scope: scope, facts: group.facts})
	}
	return out
}

func dedupeFacts(facts []string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(facts))
	for _, fact := range facts {
		normalized := normalizeCompactionFact(fact)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out
}

func normalizeCompactionFact(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}
