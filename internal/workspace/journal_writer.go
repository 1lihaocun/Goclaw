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

type JournalWriteInput struct {
	ProfileID        domain.ProfileID
	PersonID         domain.PersonID
	RoomID           domain.RoomID
	ConversationSlug string
	Scope            memory.ScopeKind
	Records          []memory.WriteRecord
}

func (l *Loader) AppendJournal(_ context.Context, input JournalWriteInput) error {
	if !l.Enabled() || len(input.Records) == 0 {
		return nil
	}

	basePath, err := l.journalBasePath(input)
	if err != nil {
		return err
	}

	grouped := make(map[string][]memory.WriteRecord)
	dayKeys := make([]string, 0, len(input.Records))
	for _, record := range input.Records {
		timestamp := record.CreatedAt.UTC()
		if timestamp.IsZero() {
			timestamp = time.Now().UTC()
		}
		dayKey := timestamp.Format("2006-01-02")
		if _, ok := grouped[dayKey]; !ok {
			dayKeys = append(dayKeys, dayKey)
		}
		record.CreatedAt = timestamp
		grouped[dayKey] = append(grouped[dayKey], record)
	}
	sort.Strings(dayKeys)

	for _, dayKey := range dayKeys {
		journalPath, err := l.resolvePath(append(basePath, "memory", dayKey+".md")...)
		if err != nil {
			return err
		}
		if err := appendJournalFile(journalPath, renderJournalRecords(grouped[dayKey])); err != nil {
			return err
		}
	}

	return nil
}

func (l *Loader) journalBasePath(input JournalWriteInput) ([]string, error) {
	switch input.Scope {
	case memory.ScopeConversation:
		if strings.TrimSpace(input.ConversationSlug) == "" {
			return nil, fmt.Errorf("workspace: conversation slug is required for conversation journal writes")
		}
		return []string{"conversations", string(input.RoomID), input.ConversationSlug}, nil
	case memory.ScopeRoom:
		return []string{"rooms", string(input.RoomID)}, nil
	case memory.ScopePersonPrivate:
		return []string{"persons", string(input.PersonID)}, nil
	default:
		return nil, fmt.Errorf("workspace: unsupported markdown journal scope %q", input.Scope)
	}
}

func appendJournalFile(path, content string) error {
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
		return fmt.Errorf("workspace: append journal %s: %w", path, err)
	}
	return nil
}

func renderJournalRecords(records []memory.WriteRecord) string {
	sorted := append([]memory.WriteRecord(nil), records...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].CreatedAt.Equal(sorted[j].CreatedAt) {
			return sorted[i].ID < sorted[j].ID
		}
		return sorted[i].CreatedAt.Before(sorted[j].CreatedAt)
	})

	var blocks []string
	for _, record := range sorted {
		role := strings.TrimSpace(record.Metadata["role"])
		if role == "" {
			role = "note"
		}
		content := strings.TrimSpace(record.Content)
		if content == "" {
			continue
		}
		blocks = append(blocks, fmt.Sprintf(
			"## %s · %s\n\n%s",
			record.CreatedAt.UTC().Format(time.RFC3339),
			role,
			content,
		))
	}
	return strings.TrimSpace(strings.Join(blocks, "\n\n")) + "\n"
}
