package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"goclaw/internal/domain"
	"goclaw/internal/memory"
)

type DurableWriteInput struct {
	ProfileID        domain.ProfileID
	PersonID         domain.PersonID
	RoomID           domain.RoomID
	ConversationID   domain.ConversationID
	ConversationSlug string
	Scope            memory.ScopeKind
	Facts            []string
	UpdatedBy        string
	Timestamp        time.Time
}

type DurableWriteResult struct {
	Path         string
	AddedFacts   []string
	SkippedFacts []string
	RemovedFacts []string
}

type DurableConflict struct {
	Group     string `json:"group"`
	Existing  string `json:"existing"`
	Candidate string `json:"candidate"`
}

type DurableConflictResult struct {
	Path      string            `json:"path"`
	Conflicts []DurableConflict `json:"conflicts,omitempty"`
}

func (l *Loader) AppendDurable(_ context.Context, input DurableWriteInput) (DurableWriteResult, error) {
	if !l.Enabled() {
		return DurableWriteResult{}, fmt.Errorf("workspace: durable memory is unavailable")
	}

	fullPath, relPath, err := l.durablePath(input)
	if err != nil {
		return DurableWriteResult{}, err
	}

	existingFacts, err := readExistingDurableFacts(fullPath)
	if err != nil {
		return DurableWriteResult{}, err
	}

	result := DurableWriteResult{Path: relPath}
	for _, fact := range input.Facts {
		normalized := normalizeDurableFact(fact)
		if normalized == "" {
			continue
		}
		if _, exists := existingFacts[normalized]; exists {
			result.SkippedFacts = append(result.SkippedFacts, normalized)
			continue
		}
		result.AddedFacts = append(result.AddedFacts, normalized)
		existingFacts[normalized] = struct{}{}
	}
	if len(result.AddedFacts) == 0 {
		return result, nil
	}

	if err := appendDurableFile(fullPath, renderDurableFacts(result.AddedFacts, input.UpdatedBy, input.Timestamp)); err != nil {
		return DurableWriteResult{}, err
	}
	return result, nil
}

func (l *Loader) ResolveDurableReview(
	_ context.Context,
	input DurableWriteInput,
	conflicts []DurableConflict,
) (DurableWriteResult, error) {
	if !l.Enabled() {
		return DurableWriteResult{}, fmt.Errorf("workspace: durable memory is unavailable")
	}

	fullPath, relPath, err := l.durablePath(input)
	if err != nil {
		return DurableWriteResult{}, err
	}
	sections, err := readDurableSections(fullPath)
	if err != nil {
		return DurableWriteResult{}, err
	}

	removeSet := make(map[string]struct{})
	for _, conflict := range conflicts {
		existing := normalizeDurableFact(conflict.Existing)
		if existing == "" {
			continue
		}
		removeSet[existing] = struct{}{}
	}

	result := DurableWriteResult{Path: relPath}
	remainingFacts := make(map[string]struct{})
	filtered := make([]durableSection, 0, len(sections))
	for _, section := range sections {
		nextFacts := make([]string, 0, len(section.Facts))
		for _, fact := range section.Facts {
			normalized := normalizeDurableFact(fact)
			if _, remove := removeSet[normalized]; remove {
				result.RemovedFacts = append(result.RemovedFacts, normalized)
				continue
			}
			nextFacts = append(nextFacts, normalized)
			remainingFacts[normalized] = struct{}{}
		}
		if len(nextFacts) == 0 {
			continue
		}
		section.Facts = nextFacts
		filtered = append(filtered, section)
	}

	for _, fact := range input.Facts {
		normalized := normalizeDurableFact(fact)
		if normalized == "" {
			continue
		}
		if _, exists := remainingFacts[normalized]; exists {
			result.SkippedFacts = append(result.SkippedFacts, normalized)
			continue
		}
		result.AddedFacts = append(result.AddedFacts, normalized)
		remainingFacts[normalized] = struct{}{}
	}

	if len(result.RemovedFacts) == 0 && len(result.AddedFacts) == 0 {
		return result, nil
	}
	if len(result.AddedFacts) > 0 {
		filtered = append(filtered, durableSection{
			Header: buildDurableHeader(input.UpdatedBy, input.Timestamp),
			Facts:  append([]string(nil), result.AddedFacts...),
		})
	}
	if err := writeDurableSections(fullPath, filtered); err != nil {
		return DurableWriteResult{}, err
	}
	return result, nil
}

func (l *Loader) CheckDurableConflicts(
	_ context.Context,
	input DurableWriteInput,
) (DurableConflictResult, error) {
	if !l.Enabled() {
		return DurableConflictResult{}, fmt.Errorf("workspace: durable memory is unavailable")
	}

	fullPath, relPath, err := l.durablePath(input)
	if err != nil {
		return DurableConflictResult{}, err
	}
	existingFacts, err := readExistingDurableFacts(fullPath)
	if err != nil {
		return DurableConflictResult{}, err
	}
	return DurableConflictResult{
		Path:      relPath,
		Conflicts: detectDurableConflicts(existingFacts, input.Facts),
	}, nil
}

func (l *Loader) durablePath(input DurableWriteInput) (string, string, error) {
	var segments []string
	switch input.Scope {
	case memory.ScopeConversation:
		if strings.TrimSpace(input.ConversationSlug) == "" {
			return "", "", fmt.Errorf("workspace: conversation slug is required for conversation durable writes")
		}
		segments = []string{"conversations", string(input.RoomID), input.ConversationSlug, "MEMORY.md"}
	case memory.ScopeRoom:
		segments = []string{"rooms", string(input.RoomID), "MEMORY.md"}
	case memory.ScopePersonSummary:
		segments = []string{"persons", string(input.PersonID), "SUMMARY.md"}
	case memory.ScopePersonPrivate:
		segments = []string{"persons", string(input.PersonID), "MEMORY.md"}
	default:
		return "", "", fmt.Errorf("workspace: unsupported durable markdown scope %q", input.Scope)
	}
	fullPath, err := l.resolvePath(segments...)
	if err != nil {
		return "", "", err
	}
	return fullPath, l.relativePath(fullPath), nil
}

func readExistingDurableFacts(path string) (map[string]struct{}, error) {
	sections, err := readDurableSections(path)
	if err != nil {
		return nil, err
	}
	facts := make(map[string]struct{})
	for _, section := range sections {
		for _, fact := range section.Facts {
			normalized := normalizeDurableFact(fact)
			if normalized == "" {
				continue
			}
			facts[normalized] = struct{}{}
		}
	}
	return facts, nil
}

type durableSection struct {
	Header string
	Facts  []string
}

func readDurableSections(path string) ([]durableSection, error) {
	content, ok, err := readOptionalFile(path)
	if err != nil {
		return nil, err
	}
	if !ok || strings.TrimSpace(content) == "" {
		return nil, nil
	}

	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	var sections []durableSection
	var current durableSection
	flush := func() {
		if strings.TrimSpace(current.Header) == "" || len(current.Facts) == 0 {
			current = durableSection{}
			return
		}
		sections = append(sections, current)
		current = durableSection{}
	}

	for _, rawLine := range lines {
		trimmed := strings.TrimSpace(rawLine)
		if strings.HasPrefix(trimmed, "## ") {
			flush()
			current = durableSection{Header: trimmed}
			continue
		}
		if current.Header == "" || !strings.HasPrefix(trimmed, "- ") {
			continue
		}
		fact := normalizeDurableFact(strings.TrimPrefix(trimmed, "- "))
		if fact == "" {
			continue
		}
		current.Facts = append(current.Facts, fact)
	}
	flush()
	return sections, nil
}

func writeDurableSections(path string, sections []durableSection) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("workspace: mkdir %s: %w", filepath.Dir(path), err)
	}
	if len(sections) == 0 {
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			return fmt.Errorf("workspace: rewrite durable memory %s: %w", path, err)
		}
		return nil
	}

	var blocks []string
	for _, section := range sections {
		if strings.TrimSpace(section.Header) == "" || len(section.Facts) == 0 {
			continue
		}
		var builder strings.Builder
		builder.WriteString(section.Header)
		builder.WriteString("\n\n")
		for _, fact := range section.Facts {
			builder.WriteString("- ")
			builder.WriteString(normalizeDurableFact(fact))
			builder.WriteString("\n")
		}
		blocks = append(blocks, strings.TrimRight(builder.String(), "\n"))
	}
	content := strings.TrimSpace(strings.Join(blocks, "\n\n")) + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("workspace: rewrite durable memory %s: %w", path, err)
	}
	return nil
}

func appendDurableFile(path, content string) error {
	if strings.TrimSpace(content) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("workspace: mkdir %s: %w", filepath.Dir(path), err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("workspace: open %s: %w", path, err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("workspace: stat %s: %w", path, err)
	}
	if info.Size() > 0 {
		if _, err := file.WriteString("\n\n"); err != nil {
			return fmt.Errorf("workspace: append separator %s: %w", path, err)
		}
	}
	if _, err := file.WriteString(content); err != nil {
		return fmt.Errorf("workspace: append durable memory %s: %w", path, err)
	}
	return nil
}

func normalizeDurableFact(value string) string {
	fields := strings.Fields(strings.TrimSpace(value))
	return strings.Join(fields, " ")
}

func detectDurableConflicts(
	existingFacts map[string]struct{},
	candidates []string,
) []DurableConflict {
	if len(existingFacts) == 0 || len(candidates) == 0 {
		return nil
	}
	existingByGroup := make(map[string][]string)
	for fact := range existingFacts {
		group := durableConflictGroup(fact)
		if group == "" {
			continue
		}
		existingByGroup[group] = append(existingByGroup[group], fact)
	}

	seen := make(map[string]struct{})
	out := make([]DurableConflict, 0, len(candidates))
	for _, candidate := range dedupeFactsForConflicts(candidates) {
		group := durableConflictGroup(candidate)
		if group == "" {
			continue
		}
		for _, existing := range existingByGroup[group] {
			if existing == candidate {
				continue
			}
			key := strings.Join([]string{group, existing, candidate}, "|")
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, DurableConflict{
				Group:     group,
				Existing:  existing,
				Candidate: candidate,
			})
		}
	}
	return out
}

func durableConflictGroup(fact string) string {
	normalized := normalizeDurableFact(fact)
	switch {
	case strings.HasPrefix(normalized, "User preference: "):
		return "user_preference"
	case strings.HasPrefix(normalized, "Preferred form of address: "):
		return "preferred_name"
	case strings.HasPrefix(normalized, "Room purpose: "):
		return "room_purpose"
	case strings.HasPrefix(normalized, "Conversation focus: "):
		return "conversation_focus"
	default:
		return ""
	}
}

func dedupeFactsForConflicts(facts []string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(facts))
	for _, fact := range facts {
		normalized := normalizeDurableFact(fact)
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

func renderDurableFacts(facts []string, updatedBy string, timestamp time.Time) string {
	var builder strings.Builder
	builder.WriteString(buildDurableHeader(updatedBy, timestamp))
	builder.WriteString("\n\n")
	for _, fact := range facts {
		builder.WriteString("- ")
		builder.WriteString(fact)
		builder.WriteString("\n")
	}
	return strings.TrimRight(builder.String(), "\n") + "\n"
}

func buildDurableHeader(updatedBy string, timestamp time.Time) string {
	if timestamp.IsZero() {
		timestamp = time.Now().UTC()
	} else {
		timestamp = timestamp.UTC()
	}
	actor := strings.TrimSpace(updatedBy)
	if actor == "" {
		actor = "goclaw"
	}
	return "## " + timestamp.Format(time.RFC3339) + " · " + actor
}
