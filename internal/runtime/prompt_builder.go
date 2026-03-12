package runtime

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"goclaw/internal/domain"
	"goclaw/internal/memory"
	sqlitestore "goclaw/internal/store/sqlite"
	workspacectx "goclaw/internal/workspace"
)

const (
	defaultTranscriptLimit = 12
	defaultMemoryLimit     = 3
)

type PromptBlock struct {
	Title   string
	Content string
}

type PromptMessage struct {
	Role    domain.MessageRole
	Content string
}

type PromptMemoryHit struct {
	Scope  memory.ScopeKind
	Result memory.SearchResult
}

type PromptPackage struct {
	SystemBlocks []PromptBlock
	Messages     []PromptMessage
	MemoryHits   []PromptMemoryHit
	Retrieval    RetrievalPlan
}

func (p PromptPackage) RenderSystemPrompt() string {
	parts := make([]string, 0, len(p.SystemBlocks))
	for _, block := range p.SystemBlocks {
		if strings.TrimSpace(block.Content) == "" {
			continue
		}
		title := strings.TrimSpace(block.Title)
		if title == "" {
			parts = append(parts, block.Content)
			continue
		}
		parts = append(parts, fmt.Sprintf("[%s]\n%s", title, block.Content))
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

type PromptBuildInput struct {
	RoomContext     RoomContext
	RecallQuery     string
	TranscriptLimit int
	MemoryLimit     int
}

type PromptBuilder struct {
	Repos     sqlitestore.Repositories
	Memory    memory.Provider
	Workspace *workspacectx.Loader
}

func NewPromptBuilder(
	repos sqlitestore.Repositories,
	provider memory.Provider,
	workspaceLoaders ...*workspacectx.Loader,
) *PromptBuilder {
	var loader *workspacectx.Loader
	if len(workspaceLoaders) > 0 {
		loader = workspaceLoaders[0]
	}
	return &PromptBuilder{
		Repos:     repos,
		Memory:    provider,
		Workspace: loader,
	}
}

func (b *PromptBuilder) Build(ctx context.Context, input PromptBuildInput) (PromptPackage, error) {
	transcriptLimit := input.TranscriptLimit
	if transcriptLimit <= 0 {
		transcriptLimit = defaultTranscriptLimit
	}

	messages, err := b.Repos.Messages.ListRecentBySession(ctx, input.RoomContext.Session.ID, transcriptLimit)
	if err != nil {
		return PromptPackage{}, err
	}
	messages = reverseMessages(messages)

	recallQuery := strings.TrimSpace(input.RecallQuery)
	if recallQuery == "" {
		recallQuery = lastNonEmptyMessage(messages)
	}

	memoryLimit := input.MemoryLimit
	if memoryLimit <= 0 {
		memoryLimit = defaultMemoryLimit
	}

	retrieval := BuildRetrievalPlan(
		input.RoomContext.Profile.ID,
		input.RoomContext.Room.ID,
		input.RoomContext.Conversation.ID,
		input.RoomContext.Person.ID,
		input.RoomContext.Consent.AccessLevel,
		input.RoomContext.ConversationSettings,
		b.anyMemoryEnabled(input.RoomContext),
	)

	providerMemoryHits, err := b.collectProviderMemoryHits(
		ctx,
		input.RoomContext,
		retrieval,
		recallQuery,
		memoryLimit,
	)
	if err != nil {
		return PromptPackage{}, err
	}
	workspaceMemoryHits, err := b.collectWorkspaceMemoryHits(
		ctx,
		input.RoomContext,
		retrieval,
		recallQuery,
		memoryLimit,
	)
	if err != nil {
		return PromptPackage{}, err
	}
	memoryHits := mergeMemoryHits(providerMemoryHits, workspaceMemoryHits)
	workspaceContext, err := b.collectWorkspaceContext(ctx, input.RoomContext)
	if err != nil {
		return PromptPackage{}, err
	}

	return PromptPackage{
		SystemBlocks: buildSystemBlocks(
			input.RoomContext,
			retrieval,
			workspaceContext,
			memoryHits,
			len(messages),
		),
		Messages:   toPromptMessages(messages),
		MemoryHits: memoryHits,
		Retrieval:  retrieval,
	}, nil
}

func (b *PromptBuilder) collectProviderMemoryHits(
	ctx context.Context,
	roomContext RoomContext,
	retrieval RetrievalPlan,
	recallQuery string,
	limit int,
) ([]PromptMemoryHit, error) {
	if !retrieval.Enabled ||
		strings.TrimSpace(recallQuery) == "" ||
		b.Memory == nil ||
		!roomContext.ConversationSettings.AllowProviderMemory {
		return nil, nil
	}

	hits := make([]PromptMemoryHit, 0, len(retrieval.Scopes)*limit)
	for _, scope := range retrieval.Scopes {
		results, err := b.Memory.Search(ctx, scope, memory.SearchQuery{
			Text:  recallQuery,
			Limit: limit,
		})
		if err != nil {
			return nil, err
		}
		for _, result := range results {
			hits = append(hits, PromptMemoryHit{
				Scope:  scope.Kind,
				Result: result,
			})
		}
	}
	return hits, nil
}

func (b *PromptBuilder) collectWorkspaceMemoryHits(
	ctx context.Context,
	roomContext RoomContext,
	retrieval RetrievalPlan,
	recallQuery string,
	limit int,
) ([]PromptMemoryHit, error) {
	if !retrieval.Enabled ||
		strings.TrimSpace(recallQuery) == "" ||
		b.Workspace == nil ||
		!b.Workspace.Enabled() ||
		!roomContext.ConversationSettings.AllowMarkdownMemory {
		return nil, nil
	}
	hits, err := b.Workspace.SearchMemory(ctx, workspacectx.SearchInput{
		ProfileID:        roomContext.Profile.ID,
		PersonID:         roomContext.Person.ID,
		RoomID:           roomContext.Room.ID,
		ConversationSlug: roomContext.Conversation.Slug,
		Consent:          roomContext.Consent.AccessLevel,
		Query:            recallQuery,
		Limit:            effectiveMarkdownLimit(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]PromptMemoryHit, 0, len(hits))
	for _, hit := range hits {
		if !scopeExists(retrieval, hit.Scope) {
			continue
		}
		out = append(out, PromptMemoryHit{
			Scope:  hit.Scope,
			Result: hit.Result,
		})
	}
	return out, nil
}

func (b *PromptBuilder) collectWorkspaceContext(
	ctx context.Context,
	roomContext RoomContext,
) (workspacectx.PromptContext, error) {
	if b.Workspace == nil || !b.Workspace.Enabled() {
		return workspacectx.PromptContext{}, nil
	}
	return b.Workspace.LoadPromptContext(ctx, workspacectx.LoadInput{
		ProfileID:        roomContext.Profile.ID,
		PersonID:         roomContext.Person.ID,
		RoomID:           roomContext.Room.ID,
		ConversationSlug: roomContext.Conversation.Slug,
		Consent:          roomContext.Consent.AccessLevel,
		AllowMarkdown:    roomContext.ConversationSettings.AllowMarkdownMemory,
	})
}

func (b *PromptBuilder) memoryEnabled() bool {
	return b.Memory != nil && b.Memory.Name() != "noop"
}

func (b *PromptBuilder) anyMemoryEnabled(roomContext RoomContext) bool {
	return roomContext.ConversationSettings.Enabled &&
		((b.memoryEnabled() && roomContext.ConversationSettings.AllowProviderMemory) ||
			(b.Workspace != nil && b.Workspace.Enabled() && roomContext.ConversationSettings.AllowMarkdownMemory))
}

func buildSystemBlocks(
	roomContext RoomContext,
	retrieval RetrievalPlan,
	workspaceContext workspacectx.PromptContext,
	memoryHits []PromptMemoryHit,
	recentTranscriptCount int,
) []PromptBlock {
	blocks := []PromptBlock{
		{
			Title: "Channel",
			Content: fmt.Sprintf(
				"provider=%s\nprofile_id=%s\nreply_routing=automatic_to_current_room",
				roomContext.Room.Provider,
				roomContext.Profile.ID,
			),
		},
		{
			Title: "Room",
			Content: fmt.Sprintf(
				"name=%s\nid=%s\nprovider_room_id=%s\nkind=%s",
				roomContext.Room.Name,
				roomContext.Room.ID,
				roomContext.Room.ProviderRoomID,
				roomContext.Room.Kind,
			),
		},
		{
			Title: "Speaker",
			Content: fmt.Sprintf(
				"name=%s\nperson_id=%s\nprovider_user_id=%s",
				roomContext.Person.DisplayName,
				roomContext.Person.ID,
				roomContext.Person.ProviderUserID,
			),
		},
		{
			Title:   "Consent",
			Content: fmt.Sprintf("personal_memory_access=%s", roomContext.Consent.AccessLevel),
		},
	}

	if strings.TrimSpace(string(roomContext.Conversation.ID)) != "" {
		blocks = append(blocks, PromptBlock{
			Title: "Conversation",
			Content: fmt.Sprintf(
				"id=%s\nslug=%s\ntitle=%s",
				roomContext.Conversation.ID,
				roomContext.Conversation.Slug,
				roomContext.Conversation.Title,
			),
		})
	}

	blocks = append(blocks, toPromptBlocks(workspaceContext.RuleBlocks)...)

	if strings.TrimSpace(roomContext.Session.SummaryText) != "" {
		blocks = append(blocks, PromptBlock{
			Title:   "Room Summary",
			Content: roomContext.Session.SummaryText,
		})
	}

	if recentTranscriptCount > 0 {
		blocks = append(blocks, PromptBlock{
			Title: "Recent Transcript",
			Content: fmt.Sprintf(
				"The attached chat messages are the most recent %d room message(s), ordered oldest to newest.",
				recentTranscriptCount,
			),
		})
	}

	if retrieval.Enabled && len(retrieval.Scopes) > 0 {
		scopeNames := make([]string, 0, len(retrieval.Scopes))
		for _, scope := range retrieval.Scopes {
			scopeNames = append(scopeNames, string(scope.Kind))
		}
		blocks = append(blocks, PromptBlock{
			Title:   "Retrieval Plan",
			Content: strings.Join(scopeNames, "\n"),
		})
	}

	if len(memoryHits) > 0 {
		lines := make([]string, 0, len(memoryHits))
		for _, hit := range memoryHits {
			lines = append(lines, fmt.Sprintf("[%s] %s", hit.Scope, hit.Result.Content))
		}
		blocks = append(blocks, PromptBlock{
			Title:   "Memory Recall",
			Content: strings.Join(lines, "\n"),
		})
	}

	return blocks
}

func toPromptBlocks(blocks []workspacectx.Block) []PromptBlock {
	out := make([]PromptBlock, 0, len(blocks))
	for _, block := range blocks {
		out = append(out, PromptBlock{
			Title:   block.Title,
			Content: block.Content,
		})
	}
	return out
}

func mergeMemoryHits(groups ...[]PromptMemoryHit) []PromptMemoryHit {
	total := 0
	for _, group := range groups {
		total += len(group)
	}
	out := make([]PromptMemoryHit, 0, total)
	seen := make(map[string]struct{}, total)
	for _, group := range groups {
		for _, hit := range group {
			key := string(hit.Scope) + "|" + hit.Result.ID + "|" + strings.TrimSpace(hit.Result.Content)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, hit)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Result.Score == out[j].Result.Score {
			return out[i].Result.ID < out[j].Result.ID
		}
		return out[i].Result.Score > out[j].Result.Score
	})
	return out
}

func scopeExists(plan RetrievalPlan, scope memory.ScopeKind) bool {
	for _, item := range plan.Scopes {
		if item.Kind == scope {
			return true
		}
	}
	return false
}

func effectiveMarkdownLimit(limit int) int {
	if limit <= 0 {
		return defaultMemoryLimit
	}
	return limit * 2
}

func toPromptMessages(messages []domain.Message) []PromptMessage {
	out := make([]PromptMessage, 0, len(messages))
	for _, message := range messages {
		out = append(out, PromptMessage{
			Role:    message.Role,
			Content: message.ContentText,
		})
	}
	return out
}

func reverseMessages(messages []domain.Message) []domain.Message {
	out := append([]domain.Message(nil), messages...)
	for left, right := 0, len(out)-1; left < right; left, right = left+1, right-1 {
		out[left], out[right] = out[right], out[left]
	}
	return out
}

func lastNonEmptyMessage(messages []domain.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		content := strings.TrimSpace(messages[i].ContentText)
		if content != "" {
			return content
		}
	}
	return ""
}
