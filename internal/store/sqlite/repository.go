package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"goclaw/internal/domain"
)

var ErrNotFound = errors.New("sqlite: record not found")

type Repositories struct {
	Profiles             ProfilesRepository
	Persons              PersonsRepository
	Rooms                RoomsRepository
	RoomSessions         RoomSessionsRepository
	Conversations        ConversationsRepository
	ConversationSettings ConversationSettingsRepository
	ConsentPolicies      ConsentPoliciesRepository
	ToolPermissions      ToolPermissionPoliciesRepository
	Messages             MessagesRepository
	ReplyRuns            ReplyRunsRepository
	ToolInvocations      ToolInvocationsRepository
	ChannelEvents        ChannelEventsRepository
	MemoryJobs           MemoryJobsRepository
}

func NewRepositories(db DBTX) Repositories {
	return Repositories{
		Profiles:             ProfilesRepository{db: db},
		Persons:              PersonsRepository{db: db},
		Rooms:                RoomsRepository{db: db},
		RoomSessions:         RoomSessionsRepository{db: db},
		Conversations:        ConversationsRepository{db: db},
		ConversationSettings: ConversationSettingsRepository{db: db},
		ConsentPolicies:      ConsentPoliciesRepository{db: db},
		ToolPermissions:      ToolPermissionPoliciesRepository{db: db},
		Messages:             MessagesRepository{db: db},
		ReplyRuns:            ReplyRunsRepository{db: db},
		ToolInvocations:      ToolInvocationsRepository{db: db},
		ChannelEvents:        ChannelEventsRepository{db: db},
		MemoryJobs:           MemoryJobsRepository{db: db},
	}
}

type ProfilesRepository struct{ db DBTX }

func (r ProfilesRepository) Upsert(ctx context.Context, profile domain.Profile) error {
	now := formatTime(time.Now())
	_, err := r.db.ExecContext(
		ctx,
		`INSERT INTO profiles(id, name, config_json, created_at, updated_at)
		 VALUES (?, ?, '{}', ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   name = excluded.name,
		   updated_at = excluded.updated_at;`,
		string(profile.ID),
		profile.Name,
		now,
		now,
	)
	if err != nil {
		return fmt.Errorf("upsert profile %s: %w", profile.ID, err)
	}
	return nil
}

func (r ProfilesRepository) Get(ctx context.Context, id domain.ProfileID) (domain.Profile, error) {
	var profile domain.Profile
	err := r.db.QueryRowContext(
		ctx,
		`SELECT id, name FROM profiles WHERE id = ?;`,
		string(id),
	).Scan(&profile.ID, &profile.Name)
	if err != nil {
		return domain.Profile{}, wrapNotFound("profile", err)
	}
	return profile, nil
}

type PersonsRepository struct{ db DBTX }

func (r PersonsRepository) Upsert(ctx context.Context, person domain.Person) error {
	now := formatTime(time.Now())
	_, err := r.db.ExecContext(
		ctx,
		`INSERT INTO persons(id, provider, provider_user_id, display_name, metadata_json, created_at, updated_at)
		 VALUES (?, ?, ?, ?, '{}', ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   provider = excluded.provider,
		   provider_user_id = excluded.provider_user_id,
		   display_name = excluded.display_name,
		   updated_at = excluded.updated_at;`,
		string(person.ID),
		string(person.Provider),
		person.ProviderUserID,
		person.DisplayName,
		now,
		now,
	)
	if err != nil {
		return fmt.Errorf("upsert person %s: %w", person.ID, err)
	}
	return nil
}

func (r PersonsRepository) Get(ctx context.Context, id domain.PersonID) (domain.Person, error) {
	return scanPerson(
		r.db.QueryRowContext(
			ctx,
			`SELECT id, provider, provider_user_id, display_name FROM persons WHERE id = ?;`,
			string(id),
		),
	)
}

func (r PersonsRepository) GetByProviderUserID(
	ctx context.Context,
	provider domain.Provider,
	providerUserID string,
) (domain.Person, error) {
	return scanPerson(
		r.db.QueryRowContext(
			ctx,
			`SELECT id, provider, provider_user_id, display_name
			 FROM persons
			 WHERE provider = ? AND provider_user_id = ?;`,
			string(provider),
			providerUserID,
		),
	)
}

type RoomsRepository struct{ db DBTX }

func (r RoomsRepository) Upsert(ctx context.Context, room domain.Room) error {
	now := formatTime(time.Now())
	_, err := r.db.ExecContext(
		ctx,
		`INSERT INTO rooms(id, provider, provider_room_id, kind, name, metadata_json, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, '{}', ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   provider = excluded.provider,
		   provider_room_id = excluded.provider_room_id,
		   kind = excluded.kind,
		   name = excluded.name,
		   updated_at = excluded.updated_at;`,
		string(room.ID),
		string(room.Provider),
		room.ProviderRoomID,
		string(room.Kind),
		room.Name,
		now,
		now,
	)
	if err != nil {
		return fmt.Errorf("upsert room %s: %w", room.ID, err)
	}
	return nil
}

func (r RoomsRepository) Get(ctx context.Context, id domain.RoomID) (domain.Room, error) {
	return scanRoom(
		r.db.QueryRowContext(
			ctx,
			`SELECT id, provider, provider_room_id, kind, name FROM rooms WHERE id = ?;`,
			string(id),
		),
	)
}

func (r RoomsRepository) GetByProviderRoomID(
	ctx context.Context,
	provider domain.Provider,
	providerRoomID string,
) (domain.Room, error) {
	return scanRoom(
		r.db.QueryRowContext(
			ctx,
			`SELECT id, provider, provider_room_id, kind, name
			 FROM rooms
			 WHERE provider = ? AND provider_room_id = ?;`,
			string(provider),
			providerRoomID,
		),
	)
}

type RoomSessionsRepository struct{ db DBTX }

func (r RoomSessionsRepository) Upsert(ctx context.Context, session domain.RoomSession) error {
	lastMessageAt := formatTime(session.LastMessageAt)
	now := formatTime(time.Now())
	_, err := r.db.ExecContext(
		ctx,
		`INSERT INTO room_sessions(
		   id, profile_id, room_id, status, summary_text, active_conversation_id, last_message_at, created_at, updated_at
		 )
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   status = excluded.status,
		   summary_text = excluded.summary_text,
		   active_conversation_id = excluded.active_conversation_id,
		   last_message_at = excluded.last_message_at,
		   updated_at = excluded.updated_at;`,
		string(session.ID),
		string(session.ProfileID),
		string(session.RoomID),
		session.Status,
		session.SummaryText,
		string(session.ActiveConversationID),
		lastMessageAt,
		now,
		now,
	)
	if err != nil {
		return fmt.Errorf("upsert room session %s: %w", session.ID, err)
	}
	return nil
}

func (r RoomSessionsRepository) GetByProfileAndRoom(
	ctx context.Context,
	profileID domain.ProfileID,
	roomID domain.RoomID,
) (domain.RoomSession, error) {
	var (
		session           domain.RoomSession
		lastMessageAtText string
	)
	err := r.db.QueryRowContext(
		ctx,
		`SELECT id, profile_id, room_id, status, summary_text, active_conversation_id, last_message_at
		 FROM room_sessions
		 WHERE profile_id = ? AND room_id = ?;`,
		string(profileID),
		string(roomID),
	).Scan(
		&session.ID,
		&session.ProfileID,
		&session.RoomID,
		&session.Status,
		&session.SummaryText,
		&session.ActiveConversationID,
		&lastMessageAtText,
	)
	if err != nil {
		return domain.RoomSession{}, wrapNotFound("room session", err)
	}
	session.LastMessageAt, err = parseTime(lastMessageAtText)
	if err != nil {
		return domain.RoomSession{}, fmt.Errorf("parse room session time: %w", err)
	}
	return session, nil
}

type ConsentPoliciesRepository struct{ db DBTX }

func (r ConsentPoliciesRepository) Upsert(ctx context.Context, policy domain.ConsentPolicy) error {
	now := formatTime(time.Now())
	expiresAt := nullableTime(policy.ExpiresAt)
	id := buildConsentPolicyID(policy.ProfileID, policy.RoomID, policy.PersonID)
	updatedBy := normalizeConsentUpdatedBy(policy.UpdatedBy)
	_, err := r.db.ExecContext(
		ctx,
		`INSERT INTO consent_policies(
		   id, profile_id, room_id, person_id, access_level, updated_by_person_id, updated_by, expires_at, created_at, updated_at
		 )
		 VALUES (?, ?, ?, ?, ?, NULL, ?, ?, ?, ?)
		 ON CONFLICT(profile_id, room_id, person_id) DO UPDATE SET
		   access_level = excluded.access_level,
		   updated_by_person_id = excluded.updated_by_person_id,
		   updated_by = excluded.updated_by,
		   expires_at = excluded.expires_at,
		   updated_at = excluded.updated_at;`,
		id,
		string(policy.ProfileID),
		string(policy.RoomID),
		string(policy.PersonID),
		string(policy.AccessLevel),
		updatedBy,
		expiresAt,
		now,
		now,
	)
	if err != nil {
		return fmt.Errorf(
			"upsert consent policy %s/%s/%s: %w",
			policy.ProfileID,
			policy.RoomID,
			policy.PersonID,
			err,
		)
	}
	return nil
}

func (r ConsentPoliciesRepository) Get(
	ctx context.Context,
	profileID domain.ProfileID,
	roomID domain.RoomID,
	personID domain.PersonID,
) (domain.ConsentPolicy, error) {
	policy, err := scanConsentPolicy(
		r.db.QueryRowContext(
			ctx,
			`SELECT profile_id, room_id, person_id, access_level, updated_by, expires_at, created_at, updated_at
			 FROM consent_policies
			 WHERE profile_id = ? AND room_id = ? AND person_id = ?;`,
			string(profileID),
			string(roomID),
			string(personID),
		),
	)
	if err != nil {
		return domain.ConsentPolicy{}, wrapNotFound("consent policy", err)
	}
	policy.Explicit = true
	return policy, nil
}

func (r ConsentPoliciesRepository) Resolve(
	ctx context.Context,
	profileID domain.ProfileID,
	roomID domain.RoomID,
	personID domain.PersonID,
) (domain.ConsentPolicy, error) {
	policy, err := r.Get(ctx, profileID, roomID, personID)
	if err == nil {
		return policy, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return domain.ConsentPolicy{}, err
	}

	var roomKind domain.RoomKind
	if err := r.db.QueryRowContext(
		ctx,
		`SELECT kind FROM rooms WHERE id = ?;`,
		string(roomID),
	).Scan(&roomKind); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ConsentPolicy{}, fmt.Errorf("consent room %s: %w", roomID, ErrNotFound)
		}
		return domain.ConsentPolicy{}, fmt.Errorf("resolve consent room %s: %w", roomID, err)
	}

	return domain.ConsentPolicy{
		ProfileID:   profileID,
		RoomID:      roomID,
		PersonID:    personID,
		AccessLevel: defaultConsentAccessLevel(roomKind),
	}, nil
}

type ToolPermissionPoliciesRepository struct{ db DBTX }

func (r ToolPermissionPoliciesRepository) Upsert(
	ctx context.Context,
	policy domain.ToolPermissionPolicy,
) error {
	now := formatTime(time.Now())
	allowedCommands, err := encodeStringList(policy.AllowedCommands)
	if err != nil {
		return fmt.Errorf("encode allowed commands: %w", err)
	}
	deniedCommands, err := encodeStringList(policy.DeniedCommands)
	if err != nil {
		return fmt.Errorf("encode denied commands: %w", err)
	}
	allowedPaths, err := encodeStringList(policy.AllowedPaths)
	if err != nil {
		return fmt.Errorf("encode allowed paths: %w", err)
	}
	deniedPaths, err := encodeStringList(policy.DeniedPaths)
	if err != nil {
		return fmt.Errorf("encode denied paths: %w", err)
	}
	allowedHosts, err := encodeStringList(policy.AllowedHosts)
	if err != nil {
		return fmt.Errorf("encode allowed hosts: %w", err)
	}
	deniedHosts, err := encodeStringList(policy.DeniedHosts)
	if err != nil {
		return fmt.Errorf("encode denied hosts: %w", err)
	}
	allowedChannelTools, err := encodeStringList(policy.AllowedChannelTools)
	if err != nil {
		return fmt.Errorf("encode allowed channel tools: %w", err)
	}
	deniedChannelTools, err := encodeStringList(policy.DeniedChannelTools)
	if err != nil {
		return fmt.Errorf("encode denied channel tools: %w", err)
	}
	allowedChannelProviders, err := encodeStringList(policy.AllowedChannelProviders)
	if err != nil {
		return fmt.Errorf("encode allowed channel providers: %w", err)
	}
	deniedChannelProviders, err := encodeStringList(policy.DeniedChannelProviders)
	if err != nil {
		return fmt.Errorf("encode denied channel providers: %w", err)
	}
	allowedMCPTools, err := encodeStringList(policy.AllowedMCPTools)
	if err != nil {
		return fmt.Errorf("encode allowed mcp tools: %w", err)
	}
	deniedMCPTools, err := encodeStringList(policy.DeniedMCPTools)
	if err != nil {
		return fmt.Errorf("encode denied mcp tools: %w", err)
	}
	allowedMCPServers, err := encodeStringList(policy.AllowedMCPServers)
	if err != nil {
		return fmt.Errorf("encode allowed mcp servers: %w", err)
	}
	deniedMCPServers, err := encodeStringList(policy.DeniedMCPServers)
	if err != nil {
		return fmt.Errorf("encode denied mcp servers: %w", err)
	}

	_, err = r.db.ExecContext(
		ctx,
		`INSERT INTO tool_permission_policies(
		   id, profile_id, room_id, commands_mode, paths_mode, network_mode,
		   channel_introspection_mode, channel_read_mode, channel_write_mode, channel_sensitive_mode,
		   mcp_introspection_mode, mcp_read_mode, mcp_write_mode, mcp_sensitive_mode,
		   allowed_commands_json, denied_commands_json, allowed_paths_json, denied_paths_json,
		   allowed_hosts_json, denied_hosts_json,
		   allowed_channel_tools_json, denied_channel_tools_json,
		   allowed_channel_providers_json, denied_channel_providers_json,
		   allowed_mcp_tools_json, denied_mcp_tools_json,
		   allowed_mcp_servers_json, denied_mcp_servers_json,
		   updated_by, created_at, updated_at
		 )
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(profile_id, room_id) DO UPDATE SET
		   commands_mode = excluded.commands_mode,
		   paths_mode = excluded.paths_mode,
		   network_mode = excluded.network_mode,
		   channel_introspection_mode = excluded.channel_introspection_mode,
		   channel_read_mode = excluded.channel_read_mode,
		   channel_write_mode = excluded.channel_write_mode,
		   channel_sensitive_mode = excluded.channel_sensitive_mode,
		   mcp_introspection_mode = excluded.mcp_introspection_mode,
		   mcp_read_mode = excluded.mcp_read_mode,
		   mcp_write_mode = excluded.mcp_write_mode,
		   mcp_sensitive_mode = excluded.mcp_sensitive_mode,
		   allowed_commands_json = excluded.allowed_commands_json,
		   denied_commands_json = excluded.denied_commands_json,
		   allowed_paths_json = excluded.allowed_paths_json,
		   denied_paths_json = excluded.denied_paths_json,
		   allowed_hosts_json = excluded.allowed_hosts_json,
		   denied_hosts_json = excluded.denied_hosts_json,
		   allowed_channel_tools_json = excluded.allowed_channel_tools_json,
		   denied_channel_tools_json = excluded.denied_channel_tools_json,
		   allowed_channel_providers_json = excluded.allowed_channel_providers_json,
		   denied_channel_providers_json = excluded.denied_channel_providers_json,
		   allowed_mcp_tools_json = excluded.allowed_mcp_tools_json,
		   denied_mcp_tools_json = excluded.denied_mcp_tools_json,
		   allowed_mcp_servers_json = excluded.allowed_mcp_servers_json,
		   denied_mcp_servers_json = excluded.denied_mcp_servers_json,
		   updated_by = excluded.updated_by,
		   updated_at = excluded.updated_at;`,
		buildToolPermissionPolicyID(policy.ProfileID, policy.RoomID),
		string(policy.ProfileID),
		string(policy.RoomID),
		string(normalizeToolPermissionMode(policy.CommandsMode)),
		string(normalizeToolPermissionMode(policy.PathsMode)),
		string(normalizeToolPermissionMode(policy.NetworkMode)),
		string(normalizeToolPermissionMode(policy.ChannelIntrospectionMode)),
		string(normalizeToolPermissionMode(policy.ChannelReadMode)),
		string(normalizeToolPermissionMode(policy.ChannelWriteMode)),
		string(normalizeToolPermissionMode(policy.ChannelSensitiveMode)),
		string(normalizeToolPermissionMode(policy.MCPIntrospectionMode)),
		string(normalizeToolPermissionMode(policy.MCPReadMode)),
		string(normalizeToolPermissionMode(policy.MCPWriteMode)),
		string(normalizeToolPermissionMode(policy.MCPSensitiveMode)),
		allowedCommands,
		deniedCommands,
		allowedPaths,
		deniedPaths,
		allowedHosts,
		deniedHosts,
		allowedChannelTools,
		deniedChannelTools,
		allowedChannelProviders,
		deniedChannelProviders,
		allowedMCPTools,
		deniedMCPTools,
		allowedMCPServers,
		deniedMCPServers,
		normalizeToolPermissionUpdatedBy(policy.UpdatedBy),
		now,
		now,
	)
	if err != nil {
		return fmt.Errorf("upsert tool permission policy %s/%s: %w", policy.ProfileID, policy.RoomID, err)
	}
	return nil
}

func (r ToolPermissionPoliciesRepository) Get(
	ctx context.Context,
	profileID domain.ProfileID,
	roomID domain.RoomID,
) (domain.ToolPermissionPolicy, error) {
	policy, err := scanToolPermissionPolicy(
		r.db.QueryRowContext(
			ctx,
			`SELECT profile_id, room_id, commands_mode, paths_mode, network_mode,
			        channel_introspection_mode, channel_read_mode, channel_write_mode, channel_sensitive_mode,
			        mcp_introspection_mode, mcp_read_mode, mcp_write_mode, mcp_sensitive_mode,
			        allowed_commands_json, denied_commands_json, allowed_paths_json, denied_paths_json,
			        allowed_hosts_json, denied_hosts_json,
			        allowed_channel_tools_json, denied_channel_tools_json,
			        allowed_channel_providers_json, denied_channel_providers_json,
			        allowed_mcp_tools_json, denied_mcp_tools_json,
			        allowed_mcp_servers_json, denied_mcp_servers_json,
			        updated_by, created_at, updated_at
			 FROM tool_permission_policies
			 WHERE profile_id = ? AND room_id = ?;`,
			string(profileID),
			string(roomID),
		),
	)
	if err != nil {
		return domain.ToolPermissionPolicy{}, wrapNotFound("tool permission policy", err)
	}
	policy.Explicit = true
	provider, providerErr := r.roomProvider(ctx, roomID)
	if providerErr != nil {
		return domain.ToolPermissionPolicy{}, providerErr
	}
	return inheritCurrentProviderForChannelAllowLists(policy, provider), nil
}

func (r ToolPermissionPoliciesRepository) Resolve(
	ctx context.Context,
	profileID domain.ProfileID,
	roomID domain.RoomID,
) (domain.ToolPermissionPolicy, error) {
	policy, err := r.Get(ctx, profileID, roomID)
	if err == nil {
		return policy, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return domain.ToolPermissionPolicy{}, err
	}

	var provider domain.Provider
	var providerErr error
	provider, providerErr = r.roomProvider(ctx, roomID)
	if providerErr != nil {
		return domain.ToolPermissionPolicy{}, providerErr
	}
	return defaultToolPermissionPolicy(profileID, roomID, provider), nil
}

func (r ToolPermissionPoliciesRepository) roomProvider(
	ctx context.Context,
	roomID domain.RoomID,
) (domain.Provider, error) {
	var provider domain.Provider
	if err := r.db.QueryRowContext(ctx, `SELECT provider FROM rooms WHERE id = ?;`, string(roomID)).Scan(&provider); err != nil {
		return "", fmt.Errorf("resolve tool permission room %s: %w", roomID, wrapNotFound("room", err))
	}
	return provider, nil
}

type MessagesRepository struct{ db DBTX }

func (r MessagesRepository) Insert(ctx context.Context, message domain.Message) error {
	_, err := r.db.ExecContext(
		ctx,
		`INSERT INTO messages(
		   id, profile_id, room_id, conversation_id, person_id, session_id, provider_message_id, role, content_text, raw_json, created_at
		 )
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);`,
		string(message.ID),
		string(message.ProfileID),
		string(message.RoomID),
		string(message.ConversationID),
		string(message.PersonID),
		string(message.SessionID),
		message.ProviderMessageID,
		string(message.Role),
		message.ContentText,
		normalizeJSONObject(message.RawJSON),
		formatTime(message.CreatedAt),
	)
	if err != nil {
		return fmt.Errorf("insert message %s: %w", message.ID, err)
	}
	return nil
}

func (r MessagesRepository) GetByProfileProviderMessageID(
	ctx context.Context,
	profileID domain.ProfileID,
	providerMessageID string,
) (domain.Message, error) {
	var (
		message       domain.Message
		createdAtText string
	)
	err := r.db.QueryRowContext(
		ctx,
		`SELECT id, profile_id, room_id, conversation_id, person_id, session_id, provider_message_id, role, content_text, raw_json, created_at
		 FROM messages
		 WHERE profile_id = ? AND provider_message_id = ?;`,
		string(profileID),
		providerMessageID,
	).Scan(
		&message.ID,
		&message.ProfileID,
		&message.RoomID,
		&message.ConversationID,
		&message.PersonID,
		&message.SessionID,
		&message.ProviderMessageID,
		&message.Role,
		&message.ContentText,
		&message.RawJSON,
		&createdAtText,
	)
	if err != nil {
		return domain.Message{}, wrapNotFound("message", err)
	}
	parsedCreatedAt, err := parseTime(createdAtText)
	if err != nil {
		return domain.Message{}, fmt.Errorf("parse message time: %w", err)
	}
	message.CreatedAt = parsedCreatedAt
	return message, nil
}

func (r MessagesRepository) UpdateContentAndRawJSONByProfileProviderMessageID(
	ctx context.Context,
	profileID domain.ProfileID,
	providerMessageID string,
	contentText string,
	rawJSON string,
) error {
	result, err := r.db.ExecContext(
		ctx,
		`UPDATE messages
		 SET content_text = ?, raw_json = ?
		 WHERE profile_id = ? AND provider_message_id = ?;`,
		contentText,
		normalizeJSONObject(rawJSON),
		string(profileID),
		providerMessageID,
	)
	if err != nil {
		return fmt.Errorf("update message %s: %w", providerMessageID, err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("update message %s rows affected: %w", providerMessageID, err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("message: %w", ErrNotFound)
	}
	return nil
}

func (r MessagesRepository) ListRecentByRoom(
	ctx context.Context,
	roomID domain.RoomID,
	limit int,
) ([]domain.Message, error) {
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT id, profile_id, room_id, conversation_id, person_id, session_id, provider_message_id, role, content_text, raw_json, created_at
		 FROM messages
		 WHERE room_id = ?
		 ORDER BY created_at DESC, id DESC
		 LIMIT ?;`,
		string(roomID),
		clampLimit(limit),
	)
	if err != nil {
		return nil, fmt.Errorf("list recent messages for room %s: %w", roomID, err)
	}
	return scanMessages(rows, "recent room messages")
}

func (r MessagesRepository) ListRecent(
	ctx context.Context,
	limit int,
) ([]domain.Message, error) {
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT id, profile_id, room_id, conversation_id, person_id, session_id, provider_message_id, role, content_text, raw_json, created_at
		 FROM messages
		 ORDER BY created_at DESC, id DESC
		 LIMIT ?;`,
		clampLimit(limit),
	)
	if err != nil {
		return nil, fmt.Errorf("list recent messages: %w", err)
	}
	return scanMessages(rows, "recent messages")
}

func (r MessagesRepository) ListRecentByConversation(
	ctx context.Context,
	conversationID domain.ConversationID,
	limit int,
) ([]domain.Message, error) {
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT id, profile_id, room_id, conversation_id, person_id, session_id, provider_message_id, role, content_text, raw_json, created_at
		 FROM messages
		 WHERE conversation_id = ?
		 ORDER BY created_at DESC, id DESC
		 LIMIT ?;`,
		string(conversationID),
		clampLimit(limit),
	)
	if err != nil {
		return nil, fmt.Errorf("list recent messages for conversation %s: %w", conversationID, err)
	}
	return scanMessages(rows, "recent conversation messages")
}

func (r MessagesRepository) ListRecentByPerson(
	ctx context.Context,
	personID domain.PersonID,
	limit int,
) ([]domain.Message, error) {
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT id, profile_id, room_id, conversation_id, person_id, session_id, provider_message_id, role, content_text, raw_json, created_at
		 FROM messages
		 WHERE person_id = ?
		 ORDER BY created_at DESC, id DESC
		 LIMIT ?;`,
		string(personID),
		clampLimit(limit),
	)
	if err != nil {
		return nil, fmt.Errorf("list recent messages for person %s: %w", personID, err)
	}
	return scanMessages(rows, "recent person messages")
}

func (r MessagesRepository) ListRecentBySession(
	ctx context.Context,
	sessionID domain.SessionID,
	limit int,
) ([]domain.Message, error) {
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT id, profile_id, room_id, conversation_id, person_id, session_id, provider_message_id, role, content_text, raw_json, created_at
		 FROM messages
		 WHERE session_id = ?
		 ORDER BY created_at DESC, id DESC
		 LIMIT ?;`,
		string(sessionID),
		clampLimit(limit),
	)
	if err != nil {
		return nil, fmt.Errorf("list recent messages for session %s: %w", sessionID, err)
	}
	return scanMessages(rows, "recent session messages")
}

func (r MessagesRepository) ListCompactedBySession(
	ctx context.Context,
	sessionID domain.SessionID,
	keepRecent int,
) ([]domain.Message, error) {
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT id, profile_id, room_id, conversation_id, person_id, session_id, provider_message_id, role, content_text, raw_json, created_at
		 FROM (
		   SELECT id, profile_id, room_id, conversation_id, person_id, session_id, provider_message_id, role, content_text, raw_json, created_at
		   FROM messages
		   WHERE session_id = ?
		   ORDER BY created_at DESC, id DESC
		   LIMIT -1 OFFSET ?
		 )
		 ORDER BY created_at ASC, id ASC;`,
		string(sessionID),
		clampOffset(keepRecent),
	)
	if err != nil {
		return nil, fmt.Errorf("list compacted messages for session %s: %w", sessionID, err)
	}
	return scanMessages(rows, "compacted session messages")
}

type ChannelEventsRepository struct{ db DBTX }

func (r ChannelEventsRepository) Insert(ctx context.Context, event domain.ChannelEvent) error {
	_, err := r.db.ExecContext(
		ctx,
		`INSERT INTO channel_events(
		   id, provider, profile_id, event_id, event_type, room_id, provider_room_id, person_id, provider_user_id, provider_message_id, payload_json, occurred_at, created_at
		 )
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);`,
		event.ID,
		string(event.Provider),
		string(event.ProfileID),
		event.EventID,
		event.Type,
		string(event.RoomID),
		event.ProviderRoomID,
		string(event.PersonID),
		event.ProviderUserID,
		event.ProviderMessageID,
		normalizeJSONObject(event.PayloadJSON),
		formatTime(event.OccurredAt),
		formatTime(event.CreatedAt),
	)
	if err != nil {
		return fmt.Errorf("insert channel event %s: %w", event.ID, err)
	}
	return nil
}

type MemoryJobsRepository struct{ db DBTX }

func (r MemoryJobsRepository) Insert(ctx context.Context, job domain.MemoryJob) error {
	_, err := r.db.ExecContext(
		ctx,
		`INSERT INTO memory_jobs(
		   id, kind, target_type, target_id, source_message_id, payload_json, status, error_text, created_at, updated_at
		 )
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);`,
		job.ID,
		job.Kind,
		job.TargetType,
		job.TargetID,
		string(job.SourceMessageID),
		normalizeJSONObject(job.PayloadJSON),
		string(job.Status),
		job.ErrorText,
		formatTime(job.CreatedAt),
		formatTime(job.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("insert memory job %s: %w", job.ID, err)
	}
	return nil
}

func (r MemoryJobsRepository) InsertOrIgnore(ctx context.Context, job domain.MemoryJob) (bool, error) {
	result, err := r.db.ExecContext(
		ctx,
		`INSERT OR IGNORE INTO memory_jobs(
		   id, kind, target_type, target_id, source_message_id, payload_json, status, error_text, created_at, updated_at
		 )
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);`,
		job.ID,
		job.Kind,
		job.TargetType,
		job.TargetID,
		string(job.SourceMessageID),
		normalizeJSONObject(job.PayloadJSON),
		string(job.Status),
		job.ErrorText,
		formatTime(job.CreatedAt),
		formatTime(job.UpdatedAt),
	)
	if err != nil {
		return false, fmt.Errorf("insert-or-ignore memory job %s: %w", job.ID, err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("insert-or-ignore memory job %s rows affected: %w", job.ID, err)
	}
	return rowsAffected > 0, nil
}

func (r MemoryJobsRepository) Get(ctx context.Context, id string) (domain.MemoryJob, error) {
	var (
		job           domain.MemoryJob
		createdAtText string
		updatedAtText string
	)
	err := r.db.QueryRowContext(
		ctx,
		`SELECT id, kind, target_type, target_id, source_message_id, payload_json, status, error_text, created_at, updated_at
		 FROM memory_jobs
		 WHERE id = ?;`,
		id,
	).Scan(
		&job.ID,
		&job.Kind,
		&job.TargetType,
		&job.TargetID,
		&job.SourceMessageID,
		&job.PayloadJSON,
		&job.Status,
		&job.ErrorText,
		&createdAtText,
		&updatedAtText,
	)
	if err != nil {
		return domain.MemoryJob{}, wrapNotFound("memory job", err)
	}
	parsedCreatedAt, err := parseTime(createdAtText)
	if err != nil {
		return domain.MemoryJob{}, fmt.Errorf("parse memory job created_at: %w", err)
	}
	parsedUpdatedAt, err := parseTime(updatedAtText)
	if err != nil {
		return domain.MemoryJob{}, fmt.Errorf("parse memory job updated_at: %w", err)
	}
	job.CreatedAt = parsedCreatedAt
	job.UpdatedAt = parsedUpdatedAt
	return job, nil
}

func (r MemoryJobsRepository) ListPending(ctx context.Context, limit int) ([]domain.MemoryJob, error) {
	return r.ListByStatus(ctx, domain.MemoryJobPending, limit)
}

func (r MemoryJobsRepository) ListByStatus(
	ctx context.Context,
	status domain.MemoryJobStatus,
	limit int,
) ([]domain.MemoryJob, error) {
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT id, kind, target_type, target_id, source_message_id, payload_json, status, error_text, created_at, updated_at
		 FROM memory_jobs
		 WHERE status = ?
		 ORDER BY created_at ASC
		 LIMIT ?;`,
		string(status),
		clampLimit(limit),
	)
	if err != nil {
		return nil, fmt.Errorf("list memory jobs by status %s: %w", status, err)
	}
	defer rows.Close()

	var jobs []domain.MemoryJob
	for rows.Next() {
		var (
			job           domain.MemoryJob
			createdAtText string
			updatedAtText string
		)
		if err := rows.Scan(
			&job.ID,
			&job.Kind,
			&job.TargetType,
			&job.TargetID,
			&job.SourceMessageID,
			&job.PayloadJSON,
			&job.Status,
			&job.ErrorText,
			&createdAtText,
			&updatedAtText,
		); err != nil {
			return nil, fmt.Errorf("scan pending memory job: %w", err)
		}
		var err error
		job.CreatedAt, err = parseTime(createdAtText)
		if err != nil {
			return nil, fmt.Errorf("parse memory job created_at: %w", err)
		}
		job.UpdatedAt, err = parseTime(updatedAtText)
		if err != nil {
			return nil, fmt.Errorf("parse memory job updated_at: %w", err)
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate memory jobs: %w", err)
	}
	return jobs, nil
}

func (r MemoryJobsRepository) StartProcessing(ctx context.Context, id string) (bool, error) {
	result, err := r.db.ExecContext(
		ctx,
		`UPDATE memory_jobs
		 SET status = ?, updated_at = ?
		 WHERE id = ? AND status = ?;`,
		string(domain.MemoryJobProcessing),
		formatTime(time.Now()),
		id,
		string(domain.MemoryJobPending),
	)
	if err != nil {
		return false, fmt.Errorf("start memory job %s processing: %w", id, err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("rows affected for memory job %s: %w", id, err)
	}
	return rowsAffected > 0, nil
}

func (r MemoryJobsRepository) UpdateStatus(
	ctx context.Context,
	id string,
	status domain.MemoryJobStatus,
	errorText string,
) error {
	_, err := r.db.ExecContext(
		ctx,
		`UPDATE memory_jobs
		 SET status = ?, error_text = ?, updated_at = ?
		 WHERE id = ?;`,
		string(status),
		errorText,
		formatTime(time.Now()),
		id,
	)
	if err != nil {
		return fmt.Errorf("update memory job %s status: %w", id, err)
	}
	return nil
}

func scanPerson(row *sql.Row) (domain.Person, error) {
	var person domain.Person
	err := row.Scan(&person.ID, &person.Provider, &person.ProviderUserID, &person.DisplayName)
	if err != nil {
		return domain.Person{}, wrapNotFound("person", err)
	}
	return person, nil
}

func scanRoom(row *sql.Row) (domain.Room, error) {
	var room domain.Room
	err := row.Scan(&room.ID, &room.Provider, &room.ProviderRoomID, &room.Kind, &room.Name)
	if err != nil {
		return domain.Room{}, wrapNotFound("room", err)
	}
	return room, nil
}

func wrapNotFound(kind string, err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%s: %w", kind, ErrNotFound)
	}
	return err
}

func scanMessages(rows *sql.Rows, contextLabel string) ([]domain.Message, error) {
	defer rows.Close()

	var messages []domain.Message
	for rows.Next() {
		var (
			message       domain.Message
			createdAtText string
		)
		if err := rows.Scan(
			&message.ID,
			&message.ProfileID,
			&message.RoomID,
			&message.ConversationID,
			&message.PersonID,
			&message.SessionID,
			&message.ProviderMessageID,
			&message.Role,
			&message.ContentText,
			&message.RawJSON,
			&createdAtText,
		); err != nil {
			return nil, fmt.Errorf("scan %s row: %w", contextLabel, err)
		}
		parsedCreatedAt, err := parseTime(createdAtText)
		if err != nil {
			return nil, fmt.Errorf("parse %s time: %w", contextLabel, err)
		}
		message.CreatedAt = parsedCreatedAt
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate %s: %w", contextLabel, err)
	}
	return messages, nil
}

func scanConsentPolicy(row *sql.Row) (domain.ConsentPolicy, error) {
	var (
		policy        domain.ConsentPolicy
		expiresAtText sql.NullString
		createdAtText string
		updatedAtText string
	)
	err := row.Scan(
		&policy.ProfileID,
		&policy.RoomID,
		&policy.PersonID,
		&policy.AccessLevel,
		&policy.UpdatedBy,
		&expiresAtText,
		&createdAtText,
		&updatedAtText,
	)
	if err != nil {
		return domain.ConsentPolicy{}, err
	}
	if expiresAtText.Valid {
		parsed, err := parseTime(expiresAtText.String)
		if err != nil {
			return domain.ConsentPolicy{}, fmt.Errorf("parse consent policy expiry: %w", err)
		}
		policy.ExpiresAt = &parsed
	}
	createdAt, err := parseTime(createdAtText)
	if err != nil {
		return domain.ConsentPolicy{}, fmt.Errorf("parse consent policy created_at: %w", err)
	}
	updatedAt, err := parseTime(updatedAtText)
	if err != nil {
		return domain.ConsentPolicy{}, fmt.Errorf("parse consent policy updated_at: %w", err)
	}
	policy.CreatedAt = createdAt
	policy.UpdatedAt = updatedAt
	return policy, nil
}

func scanToolPermissionPolicy(row *sql.Row) (domain.ToolPermissionPolicy, error) {
	var (
		policy                      domain.ToolPermissionPolicy
		allowedCommandsJSON         string
		deniedCommandsJSON          string
		allowedPathsJSON            string
		deniedPathsJSON             string
		allowedHostsJSON            string
		deniedHostsJSON             string
		allowedChannelToolsJSON     string
		deniedChannelToolsJSON      string
		allowedChannelProvidersJSON string
		deniedChannelProvidersJSON  string
		allowedMCPToolsJSON         string
		deniedMCPToolsJSON          string
		allowedMCPServersJSON       string
		deniedMCPServersJSON        string
		createdAtText               string
		updatedAtText               string
	)
	err := row.Scan(
		&policy.ProfileID,
		&policy.RoomID,
		&policy.CommandsMode,
		&policy.PathsMode,
		&policy.NetworkMode,
		&policy.ChannelIntrospectionMode,
		&policy.ChannelReadMode,
		&policy.ChannelWriteMode,
		&policy.ChannelSensitiveMode,
		&policy.MCPIntrospectionMode,
		&policy.MCPReadMode,
		&policy.MCPWriteMode,
		&policy.MCPSensitiveMode,
		&allowedCommandsJSON,
		&deniedCommandsJSON,
		&allowedPathsJSON,
		&deniedPathsJSON,
		&allowedHostsJSON,
		&deniedHostsJSON,
		&allowedChannelToolsJSON,
		&deniedChannelToolsJSON,
		&allowedChannelProvidersJSON,
		&deniedChannelProvidersJSON,
		&allowedMCPToolsJSON,
		&deniedMCPToolsJSON,
		&allowedMCPServersJSON,
		&deniedMCPServersJSON,
		&policy.UpdatedBy,
		&createdAtText,
		&updatedAtText,
	)
	if err != nil {
		return domain.ToolPermissionPolicy{}, err
	}

	policy.CommandsMode = normalizeToolPermissionMode(policy.CommandsMode)
	policy.PathsMode = normalizeToolPermissionMode(policy.PathsMode)
	policy.NetworkMode = normalizeToolPermissionMode(policy.NetworkMode)
	policy.ChannelIntrospectionMode = normalizeToolPermissionMode(policy.ChannelIntrospectionMode)
	policy.ChannelReadMode = normalizeToolPermissionMode(policy.ChannelReadMode)
	policy.ChannelWriteMode = normalizeToolPermissionMode(policy.ChannelWriteMode)
	policy.ChannelSensitiveMode = normalizeToolPermissionMode(policy.ChannelSensitiveMode)
	policy.MCPIntrospectionMode = normalizeToolPermissionMode(policy.MCPIntrospectionMode)
	policy.MCPReadMode = normalizeToolPermissionMode(policy.MCPReadMode)
	policy.MCPWriteMode = normalizeToolPermissionMode(policy.MCPWriteMode)
	policy.MCPSensitiveMode = normalizeToolPermissionMode(policy.MCPSensitiveMode)

	policy.AllowedCommands, err = decodeStringList(allowedCommandsJSON)
	if err != nil {
		return domain.ToolPermissionPolicy{}, fmt.Errorf("decode allowed commands: %w", err)
	}
	policy.DeniedCommands, err = decodeStringList(deniedCommandsJSON)
	if err != nil {
		return domain.ToolPermissionPolicy{}, fmt.Errorf("decode denied commands: %w", err)
	}
	policy.AllowedPaths, err = decodeStringList(allowedPathsJSON)
	if err != nil {
		return domain.ToolPermissionPolicy{}, fmt.Errorf("decode allowed paths: %w", err)
	}
	policy.DeniedPaths, err = decodeStringList(deniedPathsJSON)
	if err != nil {
		return domain.ToolPermissionPolicy{}, fmt.Errorf("decode denied paths: %w", err)
	}
	policy.AllowedHosts, err = decodeStringList(allowedHostsJSON)
	if err != nil {
		return domain.ToolPermissionPolicy{}, fmt.Errorf("decode allowed hosts: %w", err)
	}
	policy.DeniedHosts, err = decodeStringList(deniedHostsJSON)
	if err != nil {
		return domain.ToolPermissionPolicy{}, fmt.Errorf("decode denied hosts: %w", err)
	}
	policy.AllowedChannelTools, err = decodeStringList(allowedChannelToolsJSON)
	if err != nil {
		return domain.ToolPermissionPolicy{}, fmt.Errorf("decode allowed channel tools: %w", err)
	}
	policy.DeniedChannelTools, err = decodeStringList(deniedChannelToolsJSON)
	if err != nil {
		return domain.ToolPermissionPolicy{}, fmt.Errorf("decode denied channel tools: %w", err)
	}
	policy.AllowedChannelProviders, err = decodeStringList(allowedChannelProvidersJSON)
	if err != nil {
		return domain.ToolPermissionPolicy{}, fmt.Errorf("decode allowed channel providers: %w", err)
	}
	policy.DeniedChannelProviders, err = decodeStringList(deniedChannelProvidersJSON)
	if err != nil {
		return domain.ToolPermissionPolicy{}, fmt.Errorf("decode denied channel providers: %w", err)
	}
	policy.AllowedMCPTools, err = decodeStringList(allowedMCPToolsJSON)
	if err != nil {
		return domain.ToolPermissionPolicy{}, fmt.Errorf("decode allowed mcp tools: %w", err)
	}
	policy.DeniedMCPTools, err = decodeStringList(deniedMCPToolsJSON)
	if err != nil {
		return domain.ToolPermissionPolicy{}, fmt.Errorf("decode denied mcp tools: %w", err)
	}
	policy.AllowedMCPServers, err = decodeStringList(allowedMCPServersJSON)
	if err != nil {
		return domain.ToolPermissionPolicy{}, fmt.Errorf("decode allowed mcp servers: %w", err)
	}
	policy.DeniedMCPServers, err = decodeStringList(deniedMCPServersJSON)
	if err != nil {
		return domain.ToolPermissionPolicy{}, fmt.Errorf("decode denied mcp servers: %w", err)
	}

	createdAt, err := parseTime(createdAtText)
	if err != nil {
		return domain.ToolPermissionPolicy{}, fmt.Errorf("parse tool permission policy created_at: %w", err)
	}
	updatedAt, err := parseTime(updatedAtText)
	if err != nil {
		return domain.ToolPermissionPolicy{}, fmt.Errorf("parse tool permission policy updated_at: %w", err)
	}
	policy.CreatedAt = createdAt
	policy.UpdatedAt = updatedAt
	return policy, nil
}

func defaultConsentAccessLevel(kind domain.RoomKind) domain.ConsentAccessLevel {
	if kind == domain.RoomKindDirect {
		return domain.ConsentFull
	}
	return domain.ConsentDeny
}

func normalizeConsentUpdatedBy(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "system"
	}
	return value
}

func normalizeToolPermissionUpdatedBy(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "system"
	}
	return value
}

func normalizeToolPermissionMode(value domain.ToolPermissionMode) domain.ToolPermissionMode {
	switch value {
	case domain.ToolPermissionAllowAll, domain.ToolPermissionAllowList:
		return value
	default:
		return domain.ToolPermissionDenyAll
	}
}

func normalizeJSONObject(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "{}"
	}
	return trimmed
}

func formatTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000000000Z07:00")
}

func parseTime(value string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, value)
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatTime(*value)
}

func clampLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > 500 {
		return 500
	}
	return limit
}

func clampOffset(offset int) int {
	if offset <= 0 {
		return 0
	}
	return offset
}

func encodeStringList(values []string) (string, error) {
	data, err := json.Marshal(canonicalizeStringList(values))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func decodeStringList(value string) ([]string, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	var decoded []string
	if err := json.Unmarshal([]byte(value), &decoded); err != nil {
		return nil, err
	}
	return canonicalizeStringList(decoded), nil
}

func canonicalizeStringList(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func buildConsentPolicyID(
	profileID domain.ProfileID,
	roomID domain.RoomID,
	personID domain.PersonID,
) string {
	return fmt.Sprintf("%s:%s:%s", profileID, roomID, personID)
}

func buildToolPermissionPolicyID(
	profileID domain.ProfileID,
	roomID domain.RoomID,
) string {
	return fmt.Sprintf("%s:%s", profileID, roomID)
}

func defaultToolPermissionPolicy(
	profileID domain.ProfileID,
	roomID domain.RoomID,
	provider domain.Provider,
) domain.ToolPermissionPolicy {
	return domain.ToolPermissionPolicy{
		ProfileID:                profileID,
		RoomID:                   roomID,
		CommandsMode:             domain.ToolPermissionDenyAll,
		PathsMode:                domain.ToolPermissionDenyAll,
		NetworkMode:              domain.ToolPermissionDenyAll,
		ChannelIntrospectionMode: domain.ToolPermissionAllowList,
		ChannelReadMode:          domain.ToolPermissionDenyAll,
		ChannelWriteMode:         domain.ToolPermissionDenyAll,
		ChannelSensitiveMode:     domain.ToolPermissionDenyAll,
		MCPIntrospectionMode:     domain.ToolPermissionDenyAll,
		MCPReadMode:              domain.ToolPermissionDenyAll,
		MCPWriteMode:             domain.ToolPermissionDenyAll,
		MCPSensitiveMode:         domain.ToolPermissionDenyAll,
		AllowedChannelProviders:  []string{string(provider)},
	}
}

func inheritCurrentProviderForChannelAllowLists(
	policy domain.ToolPermissionPolicy,
	provider domain.Provider,
) domain.ToolPermissionPolicy {
	if provider == "" {
		return policy
	}
	if !hasChannelAllowListMode(policy) {
		return policy
	}
	if len(policy.AllowedChannelProviders) > 0 || len(policy.AllowedChannelTools) > 0 {
		return policy
	}
	if containsStringFold(policy.DeniedChannelProviders, string(provider)) {
		return policy
	}
	policy.AllowedChannelProviders = []string{string(provider)}
	return policy
}

func hasChannelAllowListMode(policy domain.ToolPermissionPolicy) bool {
	return normalizeToolPermissionMode(policy.ChannelIntrospectionMode) == domain.ToolPermissionAllowList ||
		normalizeToolPermissionMode(policy.ChannelReadMode) == domain.ToolPermissionAllowList ||
		normalizeToolPermissionMode(policy.ChannelWriteMode) == domain.ToolPermissionAllowList ||
		normalizeToolPermissionMode(policy.ChannelSensitiveMode) == domain.ToolPermissionAllowList
}

func containsStringFold(values []string, target string) bool {
	target = strings.TrimSpace(strings.ToLower(target))
	if target == "" {
		return false
	}
	for _, value := range values {
		if strings.TrimSpace(strings.ToLower(value)) == target {
			return true
		}
	}
	return false
}
