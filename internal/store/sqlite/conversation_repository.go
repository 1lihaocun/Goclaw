package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"goclaw/internal/domain"
)

type ConversationsRepository struct{ db DBTX }

func (r ConversationsRepository) Upsert(ctx context.Context, conversation domain.Conversation) error {
	now := formatTime(time.Now())
	_, err := r.db.ExecContext(
		ctx,
		`INSERT INTO conversations(
		   id, profile_id, room_id, slug, title, status, created_at, updated_at
		 )
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   title = excluded.title,
		   status = excluded.status,
		   updated_at = excluded.updated_at;`,
		string(conversation.ID),
		string(conversation.ProfileID),
		string(conversation.RoomID),
		conversation.Slug,
		conversation.Title,
		string(normalizeConversationStatus(conversation.Status)),
		now,
		now,
	)
	if err != nil {
		return fmt.Errorf("upsert conversation %s: %w", conversation.ID, err)
	}
	return nil
}

func (r ConversationsRepository) Get(
	ctx context.Context,
	id domain.ConversationID,
) (domain.Conversation, error) {
	return scanConversation(
		r.db.QueryRowContext(
			ctx,
			`SELECT id, profile_id, room_id, slug, title, status, created_at, updated_at
			 FROM conversations
			 WHERE id = ?;`,
			string(id),
		),
	)
}

func (r ConversationsRepository) GetByProfileRoomSlug(
	ctx context.Context,
	profileID domain.ProfileID,
	roomID domain.RoomID,
	slug string,
) (domain.Conversation, error) {
	return scanConversation(
		r.db.QueryRowContext(
			ctx,
			`SELECT id, profile_id, room_id, slug, title, status, created_at, updated_at
			 FROM conversations
			 WHERE profile_id = ? AND room_id = ? AND slug = ?;`,
			string(profileID),
			string(roomID),
			slug,
		),
	)
}

func (r ConversationsRepository) ListByProfileRoom(
	ctx context.Context,
	profileID domain.ProfileID,
	roomID domain.RoomID,
) ([]domain.Conversation, error) {
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT id, profile_id, room_id, slug, title, status, created_at, updated_at
		 FROM conversations
		 WHERE profile_id = ? AND room_id = ?
		 ORDER BY created_at ASC, id ASC;`,
		string(profileID),
		string(roomID),
	)
	if err != nil {
		return nil, fmt.Errorf("list conversations for room %s/%s: %w", profileID, roomID, err)
	}
	defer rows.Close()

	var conversations []domain.Conversation
	for rows.Next() {
		var (
			conversation domain.Conversation
			createdAt    string
			updatedAt    string
		)
		if err := rows.Scan(
			&conversation.ID,
			&conversation.ProfileID,
			&conversation.RoomID,
			&conversation.Slug,
			&conversation.Title,
			&conversation.Status,
			&createdAt,
			&updatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan conversations: %w", err)
		}
		conversation.Status = normalizeConversationStatus(conversation.Status)
		parsedCreatedAt, err := parseTime(createdAt)
		if err != nil {
			return nil, fmt.Errorf("parse conversation created_at: %w", err)
		}
		parsedUpdatedAt, err := parseTime(updatedAt)
		if err != nil {
			return nil, fmt.Errorf("parse conversation updated_at: %w", err)
		}
		conversation.CreatedAt = parsedCreatedAt
		conversation.UpdatedAt = parsedUpdatedAt
		conversations = append(conversations, conversation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate conversations: %w", err)
	}
	return conversations, nil
}

type ConversationSettingsRepository struct{ db DBTX }

func (r ConversationSettingsRepository) Upsert(
	ctx context.Context,
	settings domain.ConversationSettings,
) error {
	now := formatTime(time.Now())
	readScopes, err := encodeStringList(settings.ReadScopes)
	if err != nil {
		return fmt.Errorf("encode conversation read scopes: %w", err)
	}
	writeScopes, err := encodeStringList(settings.WriteScopes)
	if err != nil {
		return fmt.Errorf("encode conversation write scopes: %w", err)
	}
	_, err = r.db.ExecContext(
		ctx,
		`INSERT INTO conversation_settings(
		   id, conversation_id, enabled, allow_provider_memory, allow_markdown_memory,
		   read_scopes_json, write_scopes_json, write_assistant_to_person_private,
		   journal_mode, updated_by, created_at, updated_at
		 )
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(conversation_id) DO UPDATE SET
		   enabled = excluded.enabled,
		   allow_provider_memory = excluded.allow_provider_memory,
		   allow_markdown_memory = excluded.allow_markdown_memory,
		   read_scopes_json = excluded.read_scopes_json,
		   write_scopes_json = excluded.write_scopes_json,
		   write_assistant_to_person_private = excluded.write_assistant_to_person_private,
		   journal_mode = excluded.journal_mode,
		   updated_by = excluded.updated_by,
		   updated_at = excluded.updated_at;`,
		buildConversationSettingsID(settings.ConversationID),
		string(settings.ConversationID),
		boolToInt(settings.Enabled),
		boolToInt(settings.AllowProviderMemory),
		boolToInt(settings.AllowMarkdownMemory),
		readScopes,
		writeScopes,
		boolToInt(settings.WriteAssistantToPersonPrivate),
		string(normalizeConversationJournalMode(settings.JournalMode)),
		normalizeConversationSettingsUpdatedBy(settings.UpdatedBy),
		now,
		now,
	)
	if err != nil {
		return fmt.Errorf("upsert conversation settings %s: %w", settings.ConversationID, err)
	}
	return nil
}

func (r ConversationSettingsRepository) Get(
	ctx context.Context,
	conversationID domain.ConversationID,
) (domain.ConversationSettings, error) {
	settings, err := scanConversationSettings(
		r.db.QueryRowContext(
			ctx,
			`SELECT conversation_id, enabled, allow_provider_memory, allow_markdown_memory,
			        read_scopes_json, write_scopes_json, write_assistant_to_person_private,
			        journal_mode, updated_by, created_at, updated_at
			 FROM conversation_settings
			 WHERE conversation_id = ?;`,
			string(conversationID),
		),
	)
	if err != nil {
		return domain.ConversationSettings{}, wrapNotFound("conversation settings", err)
	}
	settings.Explicit = true
	return settings, nil
}

func (r ConversationSettingsRepository) Resolve(
	ctx context.Context,
	conversationID domain.ConversationID,
) (domain.ConversationSettings, error) {
	settings, err := r.Get(ctx, conversationID)
	if err == nil {
		return settings, nil
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		return domain.ConversationSettings{}, err
	}

	var exists int
	if err := r.db.QueryRowContext(
		ctx,
		`SELECT 1 FROM conversations WHERE id = ?;`,
		string(conversationID),
	).Scan(&exists); err != nil {
		if err == sql.ErrNoRows {
			return domain.ConversationSettings{}, fmt.Errorf("conversation %s: %w", conversationID, ErrNotFound)
		}
		return domain.ConversationSettings{}, fmt.Errorf("resolve conversation settings %s: %w", conversationID, err)
	}
	return defaultConversationSettings(conversationID), nil
}

func scanConversation(row *sql.Row) (domain.Conversation, error) {
	var (
		conversation domain.Conversation
		createdAt    string
		updatedAt    string
	)
	err := row.Scan(
		&conversation.ID,
		&conversation.ProfileID,
		&conversation.RoomID,
		&conversation.Slug,
		&conversation.Title,
		&conversation.Status,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		return domain.Conversation{}, wrapNotFound("conversation", err)
	}
	conversation.Status = normalizeConversationStatus(conversation.Status)
	conversation.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return domain.Conversation{}, fmt.Errorf("parse conversation created_at: %w", err)
	}
	conversation.UpdatedAt, err = parseTime(updatedAt)
	if err != nil {
		return domain.Conversation{}, fmt.Errorf("parse conversation updated_at: %w", err)
	}
	return conversation, nil
}

func scanConversationSettings(row *sql.Row) (domain.ConversationSettings, error) {
	var (
		settings                domain.ConversationSettings
		enabled                 int
		allowProviderMemory     int
		allowMarkdownMemory     int
		writeAssistantToPrivate int
		readScopesJSON          string
		writeScopesJSON         string
		createdAt               string
		updatedAt               string
	)
	err := row.Scan(
		&settings.ConversationID,
		&enabled,
		&allowProviderMemory,
		&allowMarkdownMemory,
		&readScopesJSON,
		&writeScopesJSON,
		&writeAssistantToPrivate,
		&settings.JournalMode,
		&settings.UpdatedBy,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		return domain.ConversationSettings{}, err
	}
	settings.Enabled = enabled != 0
	settings.AllowProviderMemory = allowProviderMemory != 0
	settings.AllowMarkdownMemory = allowMarkdownMemory != 0
	settings.WriteAssistantToPersonPrivate = writeAssistantToPrivate != 0
	settings.ReadScopes, err = decodeStringList(readScopesJSON)
	if err != nil {
		return domain.ConversationSettings{}, fmt.Errorf("decode conversation read scopes: %w", err)
	}
	settings.WriteScopes, err = decodeStringList(writeScopesJSON)
	if err != nil {
		return domain.ConversationSettings{}, fmt.Errorf("decode conversation write scopes: %w", err)
	}
	settings.JournalMode = normalizeConversationJournalMode(settings.JournalMode)
	settings.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return domain.ConversationSettings{}, fmt.Errorf("parse conversation settings created_at: %w", err)
	}
	settings.UpdatedAt, err = parseTime(updatedAt)
	if err != nil {
		return domain.ConversationSettings{}, fmt.Errorf("parse conversation settings updated_at: %w", err)
	}
	return settings, nil
}

func normalizeConversationStatus(value domain.ConversationStatus) domain.ConversationStatus {
	if value == domain.ConversationStatusActive {
		return value
	}
	return domain.ConversationStatusActive
}

func normalizeConversationJournalMode(value domain.ConversationJournalMode) domain.ConversationJournalMode {
	switch value {
	case domain.ConversationJournalOff,
		domain.ConversationJournalMarkdownOnly,
		domain.ConversationJournalBoth:
		return value
	default:
		return domain.ConversationJournalProviderOnly
	}
}

func normalizeConversationSettingsUpdatedBy(value string) string {
	if value == "" {
		return "system"
	}
	return value
}

func defaultConversationSettings(conversationID domain.ConversationID) domain.ConversationSettings {
	return domain.DefaultConversationSettings(conversationID)
}

func buildConversationSettingsID(conversationID domain.ConversationID) string {
	return fmt.Sprintf("conversation-settings:%s", conversationID)
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
