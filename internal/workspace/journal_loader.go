package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"goclaw/internal/domain"
	"goclaw/internal/memory"
)

type JournalLoadInput struct {
	PersonID         domain.PersonID
	RoomID           domain.RoomID
	ConversationSlug string
}

type JournalEntry struct {
	Scope     memory.ScopeKind
	Path      string
	Role      string
	Content   string
	CreatedAt time.Time
}

func (l *Loader) LoadJournalEntries(_ context.Context, input JournalLoadInput) ([]JournalEntry, error) {
	if !l.Enabled() {
		return nil, nil
	}

	specs := []struct {
		scope memory.ScopeKind
		base  []string
	}{
		{
			scope: memory.ScopeRoom,
			base:  []string{"rooms", string(input.RoomID)},
		},
		{
			scope: memory.ScopePersonPrivate,
			base:  []string{"persons", string(input.PersonID)},
		},
	}
	if strings.TrimSpace(input.ConversationSlug) != "" {
		specs = append(specs, struct {
			scope memory.ScopeKind
			base  []string
		}{
			scope: memory.ScopeConversation,
			base:  []string{"conversations", string(input.RoomID), input.ConversationSlug},
		})
	}

	entries := make([]JournalEntry, 0, 8)
	for _, spec := range specs {
		items, err := l.collectJournalEntries(spec.scope, spec.base)
		if err != nil {
			return nil, err
		}
		entries = append(entries, items...)
	}

	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].CreatedAt.Equal(entries[j].CreatedAt) {
			if entries[i].Path == entries[j].Path {
				return entries[i].Role < entries[j].Role
			}
			return entries[i].Path < entries[j].Path
		}
		return entries[i].CreatedAt.Before(entries[j].CreatedAt)
	})
	return entries, nil
}

func (l *Loader) collectJournalEntries(
	scope memory.ScopeKind,
	base []string,
) ([]JournalEntry, error) {
	memoryDir, err := l.resolvePath(append(base, "memory")...)
	if err != nil {
		return nil, err
	}
	dirEntries, err := os.ReadDir(memoryDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("workspace: read dir %s: %w", memoryDir, err)
	}

	fileNames := make([]string, 0, len(dirEntries))
	for _, entry := range dirEntries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".md") {
			continue
		}
		fileNames = append(fileNames, name)
	}
	sort.Strings(fileNames)

	out := make([]JournalEntry, 0, len(fileNames))
	for _, name := range fileNames {
		fullPath := filepath.Join(memoryDir, name)
		content, ok, err := readOptionalFile(fullPath)
		if err != nil {
			return nil, err
		}
		if !ok || strings.TrimSpace(content) == "" {
			continue
		}
		out = append(out, parseJournalEntries(scope, l.relativePath(fullPath), content)...)
	}
	return out, nil
}

func parseJournalEntries(scope memory.ScopeKind, relPath string, content string) []JournalEntry {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	var entries []JournalEntry
	var current JournalEntry
	var body []string
	flush := func() {
		if current.Scope == "" {
			return
		}
		current.Content = strings.TrimSpace(strings.Join(body, "\n"))
		if current.Content != "" {
			entries = append(entries, current)
		}
		current = JournalEntry{}
		body = nil
	}

	for _, rawLine := range lines {
		line := strings.TrimRight(rawLine, "\r")
		if strings.HasPrefix(line, "## ") {
			flush()
			timestamp, role, ok := parseJournalHeader(line)
			if ok {
				current = JournalEntry{
					Scope:     scope,
					Path:      relPath,
					Role:      role,
					CreatedAt: timestamp,
				}
				continue
			}
		}
		if current.Scope == "" {
			continue
		}
		body = append(body, line)
	}
	flush()
	return entries
}

func parseJournalHeader(line string) (time.Time, string, bool) {
	trimmed := strings.TrimSpace(strings.TrimPrefix(line, "## "))
	timestampText, roleText, ok := strings.Cut(trimmed, " · ")
	if !ok {
		return time.Time{}, "", false
	}
	timestamp, err := time.Parse(time.RFC3339, strings.TrimSpace(timestampText))
	if err != nil {
		return time.Time{}, "", false
	}
	role := strings.TrimSpace(roleText)
	if role == "" {
		role = "note"
	}
	return timestamp.UTC(), role, true
}
