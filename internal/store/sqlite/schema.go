package sqlite

type Migration struct {
	Version    int
	Name       string
	Statements []string
}

var schemaMigrations = []Migration{
	{
		Version: 1,
		Name:    "initial_core_schema",
		Statements: []string{
			`CREATE TABLE IF NOT EXISTS schema_migrations (
				version INTEGER PRIMARY KEY,
				name TEXT NOT NULL,
				applied_at TEXT NOT NULL
			);`,
			`CREATE TABLE IF NOT EXISTS profiles (
				id TEXT PRIMARY KEY,
				name TEXT NOT NULL,
				config_json TEXT NOT NULL DEFAULT '{}',
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL
			);`,
			`CREATE TABLE IF NOT EXISTS persons (
				id TEXT PRIMARY KEY,
				provider TEXT NOT NULL,
				provider_user_id TEXT NOT NULL,
				display_name TEXT NOT NULL,
				metadata_json TEXT NOT NULL DEFAULT '{}',
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL,
				UNIQUE(provider, provider_user_id)
			);`,
			`CREATE TABLE IF NOT EXISTS rooms (
				id TEXT PRIMARY KEY,
				provider TEXT NOT NULL,
				provider_room_id TEXT NOT NULL,
				kind TEXT NOT NULL,
				name TEXT NOT NULL,
				metadata_json TEXT NOT NULL DEFAULT '{}',
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL,
				UNIQUE(provider, provider_room_id)
			);`,
			`CREATE TABLE IF NOT EXISTS room_sessions (
				id TEXT PRIMARY KEY,
				profile_id TEXT NOT NULL,
				room_id TEXT NOT NULL,
				status TEXT NOT NULL,
				summary_text TEXT NOT NULL DEFAULT '',
				last_message_at TEXT NOT NULL,
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL,
				UNIQUE(profile_id, room_id),
				FOREIGN KEY(profile_id) REFERENCES profiles(id),
				FOREIGN KEY(room_id) REFERENCES rooms(id)
			);`,
			`CREATE TABLE IF NOT EXISTS consent_policies (
				id TEXT PRIMARY KEY,
				profile_id TEXT NOT NULL,
				room_id TEXT NOT NULL,
				person_id TEXT NOT NULL,
				access_level TEXT NOT NULL,
				updated_by_person_id TEXT,
				expires_at TEXT,
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL,
				UNIQUE(profile_id, room_id, person_id),
				FOREIGN KEY(profile_id) REFERENCES profiles(id),
				FOREIGN KEY(room_id) REFERENCES rooms(id),
				FOREIGN KEY(person_id) REFERENCES persons(id),
				FOREIGN KEY(updated_by_person_id) REFERENCES persons(id)
			);`,
			`CREATE TABLE IF NOT EXISTS messages (
				id TEXT PRIMARY KEY,
				profile_id TEXT NOT NULL,
				room_id TEXT NOT NULL,
				person_id TEXT NOT NULL,
				session_id TEXT NOT NULL,
				provider_message_id TEXT NOT NULL,
				role TEXT NOT NULL,
				content_text TEXT NOT NULL,
				raw_json TEXT NOT NULL DEFAULT '{}',
				created_at TEXT NOT NULL,
				UNIQUE(profile_id, provider_message_id),
				FOREIGN KEY(profile_id) REFERENCES profiles(id),
				FOREIGN KEY(room_id) REFERENCES rooms(id),
				FOREIGN KEY(person_id) REFERENCES persons(id),
				FOREIGN KEY(session_id) REFERENCES room_sessions(id)
			);`,
			`CREATE TABLE IF NOT EXISTS memory_jobs (
				id TEXT PRIMARY KEY,
				kind TEXT NOT NULL,
				target_type TEXT NOT NULL,
				target_id TEXT NOT NULL,
				source_message_id TEXT NOT NULL,
				payload_json TEXT NOT NULL DEFAULT '{}',
				status TEXT NOT NULL,
				error_text TEXT NOT NULL DEFAULT '',
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL,
				FOREIGN KEY(source_message_id) REFERENCES messages(id)
			);`,
			`CREATE INDEX IF NOT EXISTS idx_room_sessions_profile_room ON room_sessions(profile_id, room_id);`,
			`CREATE INDEX IF NOT EXISTS idx_consent_policies_profile_room_person ON consent_policies(profile_id, room_id, person_id);`,
			`CREATE INDEX IF NOT EXISTS idx_messages_room_created_at ON messages(room_id, created_at DESC);`,
			`CREATE INDEX IF NOT EXISTS idx_messages_session_created_at ON messages(session_id, created_at DESC);`,
			`CREATE INDEX IF NOT EXISTS idx_memory_jobs_status_created_at ON memory_jobs(status, created_at ASC);`,
		},
	},
	{
		Version: 2,
		Name:    "consent_policy_audit_actor",
		Statements: []string{
			`ALTER TABLE consent_policies ADD COLUMN updated_by TEXT NOT NULL DEFAULT '';`,
			`UPDATE consent_policies
			 SET updated_by = COALESCE(updated_by_person_id, '')
			 WHERE updated_by = '';`,
		},
	},
	{
		Version: 3,
		Name:    "tool_permission_policies",
		Statements: []string{
			`CREATE TABLE IF NOT EXISTS tool_permission_policies (
				id TEXT PRIMARY KEY,
				profile_id TEXT NOT NULL,
				room_id TEXT NOT NULL,
				commands_mode TEXT NOT NULL,
				paths_mode TEXT NOT NULL,
				network_mode TEXT NOT NULL,
				allowed_commands_json TEXT NOT NULL DEFAULT '[]',
				denied_commands_json TEXT NOT NULL DEFAULT '[]',
				allowed_paths_json TEXT NOT NULL DEFAULT '[]',
				denied_paths_json TEXT NOT NULL DEFAULT '[]',
				allowed_hosts_json TEXT NOT NULL DEFAULT '[]',
				denied_hosts_json TEXT NOT NULL DEFAULT '[]',
				updated_by TEXT NOT NULL DEFAULT '',
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL,
				UNIQUE(profile_id, room_id),
				FOREIGN KEY(profile_id) REFERENCES profiles(id),
				FOREIGN KEY(room_id) REFERENCES rooms(id)
			);`,
			`CREATE INDEX IF NOT EXISTS idx_tool_permission_policies_profile_room
			 ON tool_permission_policies(profile_id, room_id);`,
		},
	},
	{
		Version: 4,
		Name:    "conversation_memory_foundation",
		Statements: []string{
			`CREATE TABLE IF NOT EXISTS conversations (
				id TEXT PRIMARY KEY,
				profile_id TEXT NOT NULL,
				room_id TEXT NOT NULL,
				slug TEXT NOT NULL,
				title TEXT NOT NULL DEFAULT '',
				status TEXT NOT NULL,
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL,
				UNIQUE(profile_id, room_id, slug),
				FOREIGN KEY(profile_id) REFERENCES profiles(id),
				FOREIGN KEY(room_id) REFERENCES rooms(id)
			);`,
			`CREATE INDEX IF NOT EXISTS idx_conversations_profile_room_slug
			 ON conversations(profile_id, room_id, slug);`,
			`CREATE TABLE IF NOT EXISTS conversation_settings (
				id TEXT PRIMARY KEY,
				conversation_id TEXT NOT NULL,
				enabled INTEGER NOT NULL DEFAULT 1,
				allow_provider_memory INTEGER NOT NULL DEFAULT 1,
				allow_markdown_memory INTEGER NOT NULL DEFAULT 1,
				read_scopes_json TEXT NOT NULL DEFAULT '[]',
				write_scopes_json TEXT NOT NULL DEFAULT '[]',
				write_assistant_to_person_private INTEGER NOT NULL DEFAULT 1,
				journal_mode TEXT NOT NULL DEFAULT 'provider_only',
				updated_by TEXT NOT NULL DEFAULT '',
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL,
				UNIQUE(conversation_id),
				FOREIGN KEY(conversation_id) REFERENCES conversations(id)
			);`,
			`CREATE INDEX IF NOT EXISTS idx_conversation_settings_conversation_id
			 ON conversation_settings(conversation_id);`,
			`ALTER TABLE messages ADD COLUMN conversation_id TEXT NOT NULL DEFAULT '';`,
			`CREATE INDEX IF NOT EXISTS idx_messages_conversation_created_at
			 ON messages(conversation_id, created_at DESC);`,
		},
	},
	{
		Version: 5,
		Name:    "channel_events",
		Statements: []string{
			`CREATE TABLE IF NOT EXISTS channel_events (
				id TEXT PRIMARY KEY,
				provider TEXT NOT NULL,
				profile_id TEXT NOT NULL,
				event_id TEXT NOT NULL,
				event_type TEXT NOT NULL,
				room_id TEXT NOT NULL DEFAULT '',
				provider_room_id TEXT NOT NULL DEFAULT '',
				person_id TEXT NOT NULL DEFAULT '',
				provider_user_id TEXT NOT NULL DEFAULT '',
				provider_message_id TEXT NOT NULL DEFAULT '',
				payload_json TEXT NOT NULL DEFAULT '{}',
				occurred_at TEXT NOT NULL,
				created_at TEXT NOT NULL,
				UNIQUE(provider, profile_id, event_id)
			);`,
			`CREATE INDEX IF NOT EXISTS idx_channel_events_profile_room_occurred_at
			 ON channel_events(profile_id, room_id, occurred_at DESC);`,
			`CREATE INDEX IF NOT EXISTS idx_channel_events_profile_message_occurred_at
			 ON channel_events(profile_id, provider_message_id, occurred_at DESC);`,
		},
	},
	{
		Version: 6,
		Name:    "tooling_audit",
		Statements: []string{
			`CREATE TABLE IF NOT EXISTS agent_reply_runs (
				id TEXT PRIMARY KEY,
				profile_id TEXT NOT NULL,
				room_id TEXT NOT NULL,
				conversation_id TEXT NOT NULL,
				session_id TEXT NOT NULL,
				inbound_message_id TEXT NOT NULL,
				model_provider TEXT NOT NULL,
				tooling_mode TEXT NOT NULL,
				status TEXT NOT NULL,
				error_text TEXT NOT NULL DEFAULT '',
				started_at TEXT NOT NULL,
				finished_at TEXT,
				FOREIGN KEY(profile_id) REFERENCES profiles(id),
				FOREIGN KEY(room_id) REFERENCES rooms(id),
				FOREIGN KEY(conversation_id) REFERENCES conversations(id),
				FOREIGN KEY(session_id) REFERENCES room_sessions(id),
				FOREIGN KEY(inbound_message_id) REFERENCES messages(id)
			);`,
			`CREATE INDEX IF NOT EXISTS idx_agent_reply_runs_profile_room_started_at
			 ON agent_reply_runs(profile_id, room_id, started_at DESC);`,
			`CREATE TABLE IF NOT EXISTS tool_invocations (
				id TEXT PRIMARY KEY,
				reply_run_id TEXT NOT NULL,
				iteration INTEGER NOT NULL,
				tool_call_id TEXT NOT NULL,
				tool_name TEXT NOT NULL,
				args_json TEXT NOT NULL DEFAULT '{}',
				decision_json TEXT NOT NULL DEFAULT '{}',
				status TEXT NOT NULL,
				result_json TEXT NOT NULL DEFAULT '{}',
				error_text TEXT NOT NULL DEFAULT '',
				started_at TEXT NOT NULL,
				finished_at TEXT,
				FOREIGN KEY(reply_run_id) REFERENCES agent_reply_runs(id)
			);`,
			`CREATE INDEX IF NOT EXISTS idx_tool_invocations_reply_run_iteration
			 ON tool_invocations(reply_run_id, iteration ASC, started_at ASC);`,
		},
	},
	{
		Version: 7,
		Name:    "room_session_active_conversation",
		Statements: []string{
			`ALTER TABLE room_sessions ADD COLUMN active_conversation_id TEXT NOT NULL DEFAULT '';`,
			`CREATE INDEX IF NOT EXISTS idx_room_sessions_active_conversation_id
			 ON room_sessions(active_conversation_id);`,
		},
	},
	{
		Version: 8,
		Name:    "channel_tool_policy_and_audit_metadata",
		Statements: []string{
			`ALTER TABLE tool_permission_policies ADD COLUMN channel_introspection_mode TEXT NOT NULL DEFAULT 'allow_list';`,
			`ALTER TABLE tool_permission_policies ADD COLUMN channel_read_mode TEXT NOT NULL DEFAULT 'deny_all';`,
			`ALTER TABLE tool_permission_policies ADD COLUMN channel_write_mode TEXT NOT NULL DEFAULT 'deny_all';`,
			`ALTER TABLE tool_permission_policies ADD COLUMN channel_sensitive_mode TEXT NOT NULL DEFAULT 'deny_all';`,
			`ALTER TABLE tool_permission_policies ADD COLUMN allowed_channel_tools_json TEXT NOT NULL DEFAULT '[]';`,
			`ALTER TABLE tool_permission_policies ADD COLUMN denied_channel_tools_json TEXT NOT NULL DEFAULT '[]';`,
			`ALTER TABLE tool_permission_policies ADD COLUMN allowed_channel_providers_json TEXT NOT NULL DEFAULT '[]';`,
			`ALTER TABLE tool_permission_policies ADD COLUMN denied_channel_providers_json TEXT NOT NULL DEFAULT '[]';`,
			`ALTER TABLE tool_invocations ADD COLUMN tool_source TEXT NOT NULL DEFAULT '';`,
			`ALTER TABLE tool_invocations ADD COLUMN provider TEXT NOT NULL DEFAULT '';`,
			`ALTER TABLE tool_invocations ADD COLUMN capability_id TEXT NOT NULL DEFAULT '';`,
		},
	},
	{
		Version: 9,
		Name:    "mcp_tool_policy",
		Statements: []string{
			`ALTER TABLE tool_permission_policies ADD COLUMN mcp_introspection_mode TEXT NOT NULL DEFAULT 'deny_all';`,
			`ALTER TABLE tool_permission_policies ADD COLUMN mcp_read_mode TEXT NOT NULL DEFAULT 'deny_all';`,
			`ALTER TABLE tool_permission_policies ADD COLUMN mcp_write_mode TEXT NOT NULL DEFAULT 'deny_all';`,
			`ALTER TABLE tool_permission_policies ADD COLUMN mcp_sensitive_mode TEXT NOT NULL DEFAULT 'deny_all';`,
			`ALTER TABLE tool_permission_policies ADD COLUMN allowed_mcp_tools_json TEXT NOT NULL DEFAULT '[]';`,
			`ALTER TABLE tool_permission_policies ADD COLUMN denied_mcp_tools_json TEXT NOT NULL DEFAULT '[]';`,
			`ALTER TABLE tool_permission_policies ADD COLUMN allowed_mcp_servers_json TEXT NOT NULL DEFAULT '[]';`,
			`ALTER TABLE tool_permission_policies ADD COLUMN denied_mcp_servers_json TEXT NOT NULL DEFAULT '[]';`,
		},
	},
}

func SchemaMigrations() []Migration {
	out := make([]Migration, 0, len(schemaMigrations))
	for _, migration := range schemaMigrations {
		next := migration
		next.Statements = append([]string(nil), migration.Statements...)
		out = append(out, next)
	}
	return out
}
