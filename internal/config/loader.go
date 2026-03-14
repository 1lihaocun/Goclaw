package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"goclaw/internal/model"
)

type LoadOptions struct {
	Path string
}

type feishuAccountEnvField struct {
	Suffix string
	Apply  func(*FeishuAccountConfig, string)
}

var feishuAccountEnvFields = []feishuAccountEnvField{
	{
		Suffix: "_VERIFICATION_TOKEN",
		Apply: func(account *FeishuAccountConfig, value string) {
			account.VerificationToken = strings.TrimSpace(value)
		},
	},
	{
		Suffix: "_CONNECTION_MODE",
		Apply: func(account *FeishuAccountConfig, value string) {
			account.ConnectionMode = strings.TrimSpace(value)
		},
	},
	{
		Suffix: "_MAX_BODY_BYTES",
		Apply: func(account *FeishuAccountConfig, value string) {
			if parsed, ok := parseInt64EnvValue(value); ok {
				account.MaxBodyBytes = parsed
			}
		},
	},
	{
		Suffix: "_WEBHOOK_HOST",
		Apply: func(account *FeishuAccountConfig, value string) {
			account.WebhookHost = strings.TrimSpace(value)
		},
	},
	{
		Suffix: "_WEBHOOK_PORT",
		Apply: func(account *FeishuAccountConfig, value string) {
			if parsed, ok := parseIntEnvValue(value); ok {
				account.WebhookPort = parsed
			}
		},
	},
	{
		Suffix: "_WEBHOOK_PATH",
		Apply: func(account *FeishuAccountConfig, value string) {
			account.WebhookPath = strings.TrimSpace(value)
		},
	},
	{
		Suffix: "_API_BASE_URL",
		Apply: func(account *FeishuAccountConfig, value string) {
			account.APIBaseURL = strings.TrimSpace(value)
		},
	},
	{
		Suffix: "_PROFILE_ID",
		Apply: func(account *FeishuAccountConfig, value string) {
			account.ProfileID = strings.TrimSpace(value)
		},
	},
	{
		Suffix: "_APP_SECRET",
		Apply: func(account *FeishuAccountConfig, value string) {
			account.AppSecret = strings.TrimSpace(value)
		},
	},
	{
		Suffix: "_APP_ID",
		Apply: func(account *FeishuAccountConfig, value string) {
			account.AppID = strings.TrimSpace(value)
		},
	},
	{
		Suffix: "_ENCRYPT_KEY",
		Apply: func(account *FeishuAccountConfig, value string) {
			account.EncryptKey = strings.TrimSpace(value)
		},
	},
	{
		Suffix: "_ENABLED",
		Apply: func(account *FeishuAccountConfig, value string) {
			if parsed, ok := parseBoolEnvValue(value); ok {
				account.Enabled = boolPtr(parsed)
			}
		},
	},
	{
		Suffix: "_NAME",
		Apply: func(account *FeishuAccountConfig, value string) {
			account.Name = strings.TrimSpace(value)
		},
	},
	{
		Suffix: "_RENDER_MODE",
		Apply: func(account *FeishuAccountConfig, value string) {
			account.RenderMode = strings.TrimSpace(value)
		},
	},
	{
		Suffix: "_STREAMING_TOOL_SUMMARIES",
		Apply: func(account *FeishuAccountConfig, value string) {
			account.StreamingToolSummaries = strings.TrimSpace(value)
		},
	},
	{
		Suffix: "_PROCESSING_ACK_EMOJI",
		Apply: func(account *FeishuAccountConfig, value string) {
			account.ProcessingAckEmoji = strings.TrimSpace(value)
		},
	},
	{
		Suffix: "_ACTIONS_PROCESSING_ACK",
		Apply: func(account *FeishuAccountConfig, value string) {
			if parsed, ok := parseBoolEnvValue(value); ok {
				account.Actions.ProcessingAck = boolPtr(parsed)
			}
		},
	},
	{
		Suffix: "_ACTIONS_MESSAGE_SEND",
		Apply: func(account *FeishuAccountConfig, value string) {
			if parsed, ok := parseBoolEnvValue(value); ok {
				account.Actions.MessageSend = boolPtr(parsed)
			}
		},
	},
	{
		Suffix: "_ACTIONS_MESSAGE_UPDATE",
		Apply: func(account *FeishuAccountConfig, value string) {
			if parsed, ok := parseBoolEnvValue(value); ok {
				account.Actions.MessageUpdate = boolPtr(parsed)
			}
		},
	},
	{
		Suffix: "_ACTIONS_MESSAGE_RECALL",
		Apply: func(account *FeishuAccountConfig, value string) {
			if parsed, ok := parseBoolEnvValue(value); ok {
				account.Actions.MessageRecall = boolPtr(parsed)
			}
		},
	},
	{
		Suffix: "_ACTIONS_REACTIONS",
		Apply: func(account *FeishuAccountConfig, value string) {
			if parsed, ok := parseBoolEnvValue(value); ok {
				account.Actions.Reactions = boolPtr(parsed)
			}
		},
	},
	{
		Suffix: "_ACTIONS_PINS",
		Apply: func(account *FeishuAccountConfig, value string) {
			if parsed, ok := parseBoolEnvValue(value); ok {
				account.Actions.Pins = boolPtr(parsed)
			}
		},
	},
	{
		Suffix: "_ACTIONS_POST_MESSAGES",
		Apply: func(account *FeishuAccountConfig, value string) {
			if parsed, ok := parseBoolEnvValue(value); ok {
				account.Actions.PostMessages = boolPtr(parsed)
			}
		},
	},
	{
		Suffix: "_ACTIONS_DOCS_READ",
		Apply: func(account *FeishuAccountConfig, value string) {
			if parsed, ok := parseBoolEnvValue(value); ok {
				account.Actions.DocsRead = boolPtr(parsed)
			}
		},
	},
}

func Load(options LoadOptions) (Config, error) {
	cfg := Default()

	configPath := resolveConfiguredPath(options.Path)
	if configPath != "" {
		if err := loadFile(&cfg, configPath); err != nil {
			return Config{}, err
		}
	}

	applyEnv(&cfg)
	cfg.SourcePath = configPath
	return cfg, nil
}

func LoadFromEnv() Config {
	cfg := Default()
	applyEnv(&cfg)
	return cfg
}

func loadFile(cfg *Config, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config file %q: %w", path, err)
	}

	if err := decodeConfigJSON(data, cfg); err != nil {
		return fmt.Errorf("decode config file %q: %w", path, err)
	}
	return nil
}

func resolveConfiguredPath(path string) string {
	configPath := strings.TrimSpace(path)
	if configPath != "" {
		return configPath
	}
	return firstPresentEnv("GOCLAW_CONFIG_PATH", "GOCLAW_CONFIG")
}

func applyEnv(cfg *Config) {
	applyAppEnv(cfg)
	applyDatabaseEnv(cfg)
	applyWorkspaceEnv(cfg)
	applyGatewayEnv(cfg)
	applyFeishuEnv(cfg)
	applyModelEnv(cfg)
	applyMemoryEnv(cfg)
	applyToolingEnv(cfg)
	applyMCPEnv(cfg)
	applySkillsEnv(cfg)
}

func applyAppEnv(cfg *Config) {
	applyStringEnv(&cfg.App.Name, "GOCLAW_APP_NAME")
	applyStringEnv(&cfg.App.Environment, "GOCLAW_APP_ENV")
}

func applyDatabaseEnv(cfg *Config) {
	applyStringEnv(&cfg.Database.Driver, "GOCLAW_DB_DRIVER")
	applyStringEnv(&cfg.Database.Path, "GOCLAW_DB_PATH")
}

func applyWorkspaceEnv(cfg *Config) {
	applyStringEnv(&cfg.Workspace.Root, "GOCLAW_WORKSPACE_ROOT")
}

func applyGatewayEnv(cfg *Config) {
	applyBoolEnv(&cfg.Gateway.Restart.Enabled, "GOCLAW_GATEWAY_RESTART_ENABLED")
	applyIntEnv(&cfg.Gateway.Restart.MaxAttempts, "GOCLAW_GATEWAY_RESTART_MAX_ATTEMPTS")
	applyIntEnv(&cfg.Gateway.Restart.InitialBackoffSeconds, "GOCLAW_GATEWAY_RESTART_INITIAL_BACKOFF_SECONDS")
	applyIntEnv(&cfg.Gateway.Restart.MaxBackoffSeconds, "GOCLAW_GATEWAY_RESTART_MAX_BACKOFF_SECONDS")
	applyBoolEnv(&cfg.Gateway.Control.Enabled, "GOCLAW_GATEWAY_CONTROL_ENABLED")
	applyStringEnv(&cfg.Gateway.Control.Host, "GOCLAW_GATEWAY_CONTROL_HOST")
	applyIntEnv(&cfg.Gateway.Control.Port, "GOCLAW_GATEWAY_CONTROL_PORT")
}

func applyFeishuEnv(cfg *Config) {
	applyFeishuTopLevelEnv(&cfg.Channels.Feishu, "GOCLAW_FEISHU_")
	applyFeishuTopLevelEnv(&cfg.Channels.Feishu, "GOCLAW_CHANNELS_FEISHU_")
	applyFeishuAccountEnv(&cfg.Channels.Feishu, "GOCLAW_CHANNELS_FEISHU_ACCOUNTS_")
}

func applyFeishuTopLevelEnv(cfg *FeishuConfig, prefix string) {
	applyBoolEnv(&cfg.Enabled, prefix+"ENABLED")
	applyStringEnv(&cfg.DefaultAccount, prefix+"DEFAULT_ACCOUNT")
	applyStringEnv(&cfg.RenderMode, prefix+"RENDER_MODE")
	applyStringEnv(&cfg.StreamingToolSummaries, prefix+"STREAMING_TOOL_SUMMARIES")
	applyBoolEnv(&cfg.Actions.ProcessingAck, prefix+"ACTIONS_PROCESSING_ACK")
	applyBoolEnv(&cfg.Actions.MessageSend, prefix+"ACTIONS_MESSAGE_SEND")
	applyBoolEnv(&cfg.Actions.MessageUpdate, prefix+"ACTIONS_MESSAGE_UPDATE")
	applyBoolEnv(&cfg.Actions.MessageRecall, prefix+"ACTIONS_MESSAGE_RECALL")
	applyBoolEnv(&cfg.Actions.Reactions, prefix+"ACTIONS_REACTIONS")
	applyBoolEnv(&cfg.Actions.Pins, prefix+"ACTIONS_PINS")
	applyBoolEnv(&cfg.Actions.PostMessages, prefix+"ACTIONS_POST_MESSAGES")
	applyBoolEnv(&cfg.Actions.DocsRead, prefix+"ACTIONS_DOCS_READ")
	applyStringEnv(&cfg.ProcessingAckEmoji, prefix+"PROCESSING_ACK_EMOJI")
	applyStringEnv(&cfg.ProfileID, prefix+"PROFILE_ID")
	applyStringEnv(&cfg.ConnectionMode, prefix+"CONNECTION_MODE")
	applyStringEnv(&cfg.WebhookHost, prefix+"WEBHOOK_HOST")
	applyIntEnv(&cfg.WebhookPort, prefix+"WEBHOOK_PORT")
	applyStringEnv(&cfg.WebhookPath, prefix+"WEBHOOK_PATH")
	applyStringEnv(&cfg.APIBaseURL, prefix+"API_BASE_URL")
	applyStringEnv(&cfg.AppID, prefix+"APP_ID")
	applyStringEnv(&cfg.AppSecret, prefix+"APP_SECRET")
	applyStringEnv(&cfg.EncryptKey, prefix+"ENCRYPT_KEY")
	applyStringEnv(&cfg.VerificationToken, prefix+"VERIFICATION_TOKEN")
	applyInt64Env(&cfg.MaxBodyBytes, prefix+"MAX_BODY_BYTES")
}

func applyFeishuAccountEnv(cfg *FeishuConfig, prefix string) {
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || !strings.HasPrefix(key, prefix) {
			continue
		}

		accountID, apply, ok := matchFeishuAccountEnvField(strings.TrimPrefix(key, prefix))
		if !ok {
			continue
		}
		if cfg.Accounts == nil {
			cfg.Accounts = make(map[string]FeishuAccountConfig)
		}

		account := cfg.Accounts[accountID]
		apply(&account, value)
		cfg.Accounts[accountID] = account
	}
}

func matchFeishuAccountEnvField(
	key string,
) (string, func(*FeishuAccountConfig, string), bool) {
	trimmedKey := strings.TrimSpace(key)
	for _, field := range feishuAccountEnvFields {
		if !strings.HasSuffix(trimmedKey, field.Suffix) {
			continue
		}
		accountID := normalizeEnvAccountID(strings.TrimSuffix(trimmedKey, field.Suffix))
		if accountID == "" {
			return "", nil, false
		}
		return accountID, field.Apply, true
	}
	return "", nil, false
}

func applyModelEnv(cfg *Config) {
	applyStringEnv(&cfg.Model.Provider, "GOCLAW_MODEL_PROVIDER")
	if value, ok := lookupTrimmedEnv("GOCLAW_MODEL_API"); ok {
		cfg.Model.API = model.API(value)
	}
	applyStringEnv(&cfg.Model.BaseURL, "GOCLAW_MODEL_BASE_URL")
	applyBoolEnv(&cfg.Model.AuthHeader, "GOCLAW_MODEL_AUTH_HEADER")
	applyStringEnv(&cfg.Model.APIKey, "GOCLAW_MODEL_API_KEY")
	applyStringEnv(&cfg.Model.ModelID, "GOCLAW_MODEL_ID")
	applyIntEnv(&cfg.Model.MaxTokens, "GOCLAW_MODEL_MAX_TOKENS")
	applyStringEnv(&cfg.Model.AnthropicVersion, "GOCLAW_ANTHROPIC_VERSION")
	applyStringEnv(&cfg.Model.AnthropicBeta, "GOCLAW_ANTHROPIC_BETA")
}

func applyMemoryEnv(cfg *Config) {
	applyStringEnv(&cfg.Memory.Provider, "GOCLAW_MEMORY_PROVIDER")
	applyBoolEnv(&cfg.Memory.MemOS.Enabled, "GOCLAW_MEMOS_ENABLED")
	applyStringEnv(&cfg.Memory.MemOS.BaseURL, "GOCLAW_MEMOS_BASE_URL")
	applyStringEnv(&cfg.Memory.MemOS.APIKey, "GOCLAW_MEMOS_API_KEY")
	applyBoolEnv(&cfg.Memory.AutoPromote.Enabled, "GOCLAW_MEMORY_AUTO_PROMOTE_ENABLED")
	applyBoolEnv(&cfg.Memory.AutoPromote.WeakPatternsEnabled, "GOCLAW_MEMORY_AUTO_PROMOTE_WEAK_PATTERNS_ENABLED")
	applyBoolEnv(&cfg.Memory.AutoPromote.StrongPatternsEnabled, "GOCLAW_MEMORY_AUTO_PROMOTE_STRONG_PATTERNS_ENABLED")
	applyIntEnv(&cfg.Memory.AutoPromote.RecentMessagesLimit, "GOCLAW_MEMORY_AUTO_PROMOTE_RECENT_MESSAGES_LIMIT")
	applyIntEnv(&cfg.Memory.AutoPromote.MinimumEvidence, "GOCLAW_MEMORY_AUTO_PROMOTE_MINIMUM_EVIDENCE")
	applyIntEnv(&cfg.Memory.AutoPromote.PollIntervalSeconds, "GOCLAW_MEMORY_AUTO_PROMOTE_POLL_INTERVAL_SECONDS")
	applyBoolEnv(&cfg.Memory.AutoPromote.Sources.Transcript, "GOCLAW_MEMORY_AUTO_PROMOTE_SOURCE_TRANSCRIPT_ENABLED")
	applyBoolEnv(&cfg.Memory.AutoPromote.Sources.Journals, "GOCLAW_MEMORY_AUTO_PROMOTE_SOURCE_JOURNALS_ENABLED")
	applyBoolEnv(&cfg.Memory.AutoPromote.Layers.Conversation, "GOCLAW_MEMORY_AUTO_PROMOTE_CONVERSATION_ENABLED")
	applyBoolEnv(&cfg.Memory.AutoPromote.Layers.Room, "GOCLAW_MEMORY_AUTO_PROMOTE_ROOM_ENABLED")
	applyBoolEnv(&cfg.Memory.AutoPromote.Layers.PersonSummary, "GOCLAW_MEMORY_AUTO_PROMOTE_PERSON_SUMMARY_ENABLED")
	applyBoolEnv(&cfg.Memory.AutoPromote.Layers.PersonPrivate, "GOCLAW_MEMORY_AUTO_PROMOTE_PERSON_PRIVATE_ENABLED")
}

func applyToolingEnv(cfg *Config) {
	applyBoolEnv(&cfg.Tooling.LegacyJSONFallbackEnabled, "GOCLAW_TOOLING_LEGACY_JSON_FALLBACK_ENABLED")
	applyIntEnv(&cfg.Tooling.MaxIterations, "GOCLAW_TOOLING_MAX_ITERATIONS")
}

func applyMCPEnv(cfg *Config) {
	applyBoolEnv(&cfg.MCP.Enabled, "GOCLAW_MCP_ENABLED")
}

func applySkillsEnv(cfg *Config) {
	applyBoolEnv(&cfg.Skills.Enabled, "GOCLAW_SKILLS_ENABLED")
	applyStringEnv(&cfg.Skills.Root, "GOCLAW_SKILLS_ROOT")
	applyIntEnv(&cfg.Skills.MaxEntries, "GOCLAW_SKILLS_MAX_ENTRIES")
	applyIntEnv(&cfg.Skills.MaxInline, "GOCLAW_SKILLS_MAX_INLINE")
	applyIntEnv(&cfg.Skills.MaxBytesPerSkill, "GOCLAW_SKILLS_MAX_BYTES_PER_SKILL")
	applyIntEnv(&cfg.Skills.MaxPromptBytes, "GOCLAW_SKILLS_MAX_PROMPT_BYTES")
}

func applyStringEnv(target *string, key string) {
	if value, ok := lookupTrimmedEnv(key); ok {
		*target = value
	}
}

func applyBoolEnv(target *bool, key string) {
	if value, ok := lookupTrimmedEnv(key); ok {
		if parsed, parsedOK := parseBoolEnvValue(value); parsedOK {
			*target = parsed
		}
	}
}

func applyIntEnv(target *int, key string) {
	if value, ok := lookupTrimmedEnv(key); ok {
		if parsed, parsedOK := parseIntEnvValue(value); parsedOK {
			*target = parsed
		}
	}
}

func applyInt64Env(target *int64, key string) {
	if value, ok := lookupTrimmedEnv(key); ok {
		if parsed, parsedOK := parseInt64EnvValue(value); parsedOK {
			*target = parsed
		}
	}
}

func lookupTrimmedEnv(key string) (string, bool) {
	value, ok := os.LookupEnv(key)
	if !ok {
		return "", false
	}
	return strings.TrimSpace(value), true
}

func firstPresentEnv(keys ...string) string {
	for _, key := range keys {
		if value, ok := lookupTrimmedEnv(key); ok && value != "" {
			return value
		}
	}
	return ""
}

func parseBoolEnvValue(value string) (bool, bool) {
	parsed, err := strconv.ParseBool(strings.TrimSpace(value))
	if err != nil {
		return false, false
	}
	return parsed, true
}

func parseIntEnvValue(value string) (int, bool) {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, false
	}
	return parsed, true
}

func parseInt64EnvValue(value string) (int64, bool) {
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return 0, false
	}
	return parsed, true
}

func normalizeEnvAccountID(value string) string {
	normalized := strings.Trim(strings.TrimSpace(value), "_")
	if normalized == "" {
		return ""
	}
	return strings.ToLower(normalized)
}

func boolPtr(value bool) *bool {
	return &value
}
