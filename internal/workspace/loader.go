package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"goclaw/internal/domain"
	"goclaw/internal/memory"
)

const (
	defaultRuleFileLimit   = 4000
	defaultMemoryFileLimit = 4000
)

type Loader struct {
	Root string
}

type Block struct {
	Title   string
	Content string
	Path    string
}

type PromptContext struct {
	RuleBlocks []Block
}

type LoadInput struct {
	ProfileID        domain.ProfileID
	PersonID         domain.PersonID
	RoomID           domain.RoomID
	ConversationSlug string
	Consent          domain.ConsentAccessLevel
	AllowMarkdown    bool
}

type SearchInput struct {
	ProfileID        domain.ProfileID
	PersonID         domain.PersonID
	RoomID           domain.RoomID
	ConversationSlug string
	Consent          domain.ConsentAccessLevel
	Query            string
	Limit            int
}

type MemoryHit struct {
	Scope  memory.ScopeKind
	Result memory.SearchResult
}

type memoryCandidate struct {
	scope   memory.ScopeKind
	title   string
	path    string
	content string
	relPath string
}

func NewLoader(root string) *Loader {
	return &Loader{Root: strings.TrimSpace(root)}
}

func (l *Loader) Enabled() bool {
	return l != nil && strings.TrimSpace(l.Root) != ""
}

func (l *Loader) LoadPromptContext(_ context.Context, input LoadInput) (PromptContext, error) {
	if !l.Enabled() {
		return PromptContext{}, nil
	}

	ruleSpecs := []struct {
		title string
		path  []string
	}{
		{title: "Profile Rules", path: []string{"profiles", string(input.ProfileID), "AGENTS.md"}},
		{title: "Profile Soul", path: []string{"profiles", string(input.ProfileID), "SOUL.md"}},
		{title: "Profile Identity", path: []string{"profiles", string(input.ProfileID), "IDENTITY.md"}},
		{title: "Profile Tools", path: []string{"profiles", string(input.ProfileID), "TOOLS.md"}},
		{title: "Room Rules", path: []string{"rooms", string(input.RoomID), "ROOM.md"}},
	}
	if strings.TrimSpace(input.ConversationSlug) != "" {
		ruleSpecs = append(ruleSpecs, struct {
			title string
			path  []string
		}{
			title: "Conversation Rules",
			path:  []string{"conversations", string(input.RoomID), input.ConversationSlug, "CONVERSATION.md"},
		})
	}
	if input.Consent == domain.ConsentSummary || input.Consent == domain.ConsentFull {
		ruleSpecs = append(ruleSpecs, struct {
			title string
			path  []string
		}{
			title: "User Profile",
			path:  []string{"persons", string(input.PersonID), "USER.md"},
		})
	}

	ruleBlocks, err := l.loadBlocks(ruleSpecs, defaultRuleFileLimit)
	if err != nil {
		return PromptContext{}, err
	}
	return PromptContext{RuleBlocks: ruleBlocks}, nil
}

func (l *Loader) SearchMemory(_ context.Context, input SearchInput) ([]MemoryHit, error) {
	if !l.Enabled() || strings.TrimSpace(input.Query) == "" {
		return nil, nil
	}
	candidates, err := l.memoryCandidates(input)
	if err != nil {
		return nil, err
	}
	hits := make([]MemoryHit, 0, len(candidates))
	for _, candidate := range candidates {
		result, ok := searchMemoryCandidate(candidate, input.Query)
		if !ok {
			continue
		}
		hits = append(hits, MemoryHit{
			Scope:  candidate.scope,
			Result: result,
		})
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Result.Score == hits[j].Result.Score {
			return hits[i].Result.ID < hits[j].Result.ID
		}
		return hits[i].Result.Score > hits[j].Result.Score
	})
	if input.Limit > 0 && len(hits) > input.Limit {
		hits = hits[:input.Limit]
	}
	return hits, nil
}

func (l *Loader) memoryCandidates(input SearchInput) ([]memoryCandidate, error) {
	candidates := make([]memoryCandidate, 0, 8)
	if strings.TrimSpace(input.ConversationSlug) != "" {
		items, err := l.collectMemoryFiles(memory.ScopeConversation, "Conversation", []string{
			"conversations", string(input.RoomID), input.ConversationSlug,
		})
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, items...)
	}
	items, err := l.collectMemoryFiles(memory.ScopeRoom, "Room", []string{
		"rooms", string(input.RoomID),
	})
	if err != nil {
		return nil, err
	}
	candidates = append(candidates, items...)
	if input.Consent == domain.ConsentSummary || input.Consent == domain.ConsentFull {
		items, err := l.collectPersonSummaryMemoryFiles(input.PersonID)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, items...)
	}
	if input.Consent == domain.ConsentFull {
		items, err := l.collectMemoryFiles(memory.ScopePersonPrivate, "Person", []string{
			"persons", string(input.PersonID),
		})
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, items...)
	}
	return candidates, nil
}

func (l *Loader) collectPersonSummaryMemoryFiles(personID domain.PersonID) ([]memoryCandidate, error) {
	summaryPath, err := l.resolvePath("persons", string(personID), "SUMMARY.md")
	if err != nil {
		return nil, err
	}
	content, ok, err := readOptionalFile(summaryPath)
	if err != nil {
		return nil, err
	}
	if !ok || strings.TrimSpace(content) == "" {
		return nil, nil
	}
	return []memoryCandidate{{
		scope:   memory.ScopePersonSummary,
		title:   "Person Summary Memory",
		path:    summaryPath,
		content: content,
		relPath: l.relativePath(summaryPath),
	}}, nil
}

func (l *Loader) collectMemoryFiles(
	scope memory.ScopeKind,
	title string,
	base []string,
) ([]memoryCandidate, error) {
	candidates := make([]memoryCandidate, 0, 4)
	mainPath, err := l.resolvePath(append(base, "MEMORY.md")...)
	if err != nil {
		return nil, err
	}
	if content, ok, err := readOptionalFile(mainPath); err != nil {
		return nil, err
	} else if ok && strings.TrimSpace(content) != "" {
		candidates = append(candidates, memoryCandidate{
			scope:   scope,
			title:   title + " Durable Memory",
			path:    mainPath,
			content: content,
			relPath: l.relativePath(mainPath),
		})
	}

	memoryDir, err := l.resolvePath(append(base, "memory")...)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(memoryDir)
	if err != nil {
		if os.IsNotExist(err) {
			return candidates, nil
		}
		return nil, fmt.Errorf("workspace: read dir %s: %w", memoryDir, err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".md") {
			continue
		}
		filePath := filepath.Join(memoryDir, name)
		content, ok, err := readOptionalFile(filePath)
		if err != nil {
			return nil, err
		}
		if !ok || strings.TrimSpace(content) == "" {
			continue
		}
		candidates = append(candidates, memoryCandidate{
			scope:   scope,
			title:   title + " Journal",
			path:    filePath,
			content: content,
			relPath: l.relativePath(filePath),
		})
	}
	return candidates, nil
}

func (l *Loader) loadBlocks(
	specs []struct {
		title string
		path  []string
	},
	limit int,
) ([]Block, error) {
	blocks := make([]Block, 0, len(specs))
	for _, spec := range specs {
		path, err := l.resolvePath(spec.path...)
		if err != nil {
			return nil, err
		}
		content, ok, err := readOptionalFile(path)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		content = truncateContent(content, limit)
		if strings.TrimSpace(content) == "" {
			continue
		}
		blocks = append(blocks, Block{
			Title:   spec.title,
			Content: content,
			Path:    path,
		})
	}
	return blocks, nil
}

func (l *Loader) resolvePath(parts ...string) (string, error) {
	root := strings.TrimSpace(l.Root)
	if root == "" {
		return "", nil
	}
	cleanParts := make([]string, 0, len(parts)+1)
	cleanParts = append(cleanParts, root)
	for _, part := range parts {
		if err := validatePathSegment(part); err != nil {
			return "", err
		}
		cleanParts = append(cleanParts, part)
	}
	return filepath.Join(cleanParts...), nil
}

func (l *Loader) relativePath(path string) string {
	if !l.Enabled() {
		return path
	}
	rel, err := filepath.Rel(l.Root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}

func validatePathSegment(value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fmt.Errorf("workspace: invalid empty path segment")
	}
	if strings.Contains(trimmed, "/") || strings.Contains(trimmed, "\\") || trimmed == "." || trimmed == ".." {
		return fmt.Errorf("workspace: invalid path segment %q", value)
	}
	return nil
}

func readOptionalFile(path string) (string, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("workspace: read %s: %w", path, err)
	}
	return string(data), true, nil
}

func truncateContent(content string, limit int) string {
	content = strings.TrimSpace(content)
	if limit <= 0 {
		return content
	}
	runes := []rune(content)
	if len(runes) <= limit {
		return content
	}
	return strings.TrimSpace(string(runes[:limit])) + "\n...[truncated]"
}

func searchMemoryCandidate(candidate memoryCandidate, query string) (memory.SearchResult, bool) {
	score, matchIndex := memoryMatchScore(candidate.content, query)
	if score <= 0 {
		return memory.SearchResult{}, false
	}
	result := memory.SearchResult{
		ID:      candidate.relPath,
		Content: memorySnippet(candidate.content, matchIndex, defaultMemoryFileLimit),
		Source:  "workspace_markdown",
		Score:   score + recencyBonusFromPath(candidate.path),
		Metadata: map[string]string{
			"path":  candidate.relPath,
			"title": candidate.title,
			"scope": string(candidate.scope),
		},
	}
	return result, true
}

func memoryMatchScore(content string, query string) (float64, int) {
	normalizedContent := strings.ToLower(content)
	normalizedQuery := strings.ToLower(strings.TrimSpace(query))
	if normalizedQuery == "" {
		return 0, -1
	}
	score := 0.0
	matchIndex := strings.Index(normalizedContent, normalizedQuery)
	if matchIndex >= 0 {
		score += 2.0
	}

	terms := queryTerms(normalizedQuery)
	if len(terms) == 0 {
		return score, matchIndex
	}
	for _, term := range terms {
		if idx := strings.Index(normalizedContent, term); idx >= 0 {
			score += 1.0
			if matchIndex < 0 {
				matchIndex = idx
			}
		}
	}
	return score, matchIndex
}

func queryTerms(query string) []string {
	fields := strings.FieldsFunc(query, func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
	})
	seen := make(map[string]struct{}, len(fields))
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if len(field) < 2 {
			continue
		}
		if _, ok := seen[field]; ok {
			continue
		}
		seen[field] = struct{}{}
		out = append(out, field)
	}
	return out
}

func memorySnippet(content string, matchIndex int, limit int) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	runes := []rune(content)
	if limit <= 0 {
		limit = defaultMemoryFileLimit
	}
	if len(runes) <= limit {
		return content
	}
	if matchIndex < 0 {
		return truncateContent(content, limit)
	}
	if matchIndex > len(runes) {
		matchIndex = len(runes)
	}
	start := matchIndex - (limit / 3)
	if start < 0 {
		start = 0
	}
	end := start + limit
	if end > len(runes) {
		end = len(runes)
		if end-limit > 0 {
			start = end - limit
		} else {
			start = 0
		}
	}
	snippet := strings.TrimSpace(string(runes[start:end]))
	if start > 0 {
		snippet = "..." + snippet
	}
	if end < len(runes) {
		snippet += "..."
	}
	return snippet
}

func recencyBonusFromPath(path string) float64 {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if len(base) != len("2006-01-02") {
		return 0
	}
	year, err := strconv.Atoi(base[0:4])
	if err != nil {
		return 0
	}
	month, err := strconv.Atoi(base[5:7])
	if err != nil {
		return 0
	}
	day, err := strconv.Atoi(base[8:10])
	if err != nil {
		return 0
	}
	date := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	days := int(time.Since(date).Hours() / 24)
	switch {
	case days <= 0:
		return 0.5
	case days <= 3:
		return 0.3
	case days <= 7:
		return 0.15
	default:
		return 0
	}
}
