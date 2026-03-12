package config

import "goclaw/internal/model"

type Config struct {
	App       AppConfig       `json:"app"`
	Database  DatabaseConfig  `json:"database"`
	Workspace WorkspaceConfig `json:"workspace"`
	Channels  ChannelsConfig  `json:"channels"`
	Model     ModelConfig     `json:"model"`
	Memory    MemoryConfig    `json:"memory"`
	Tooling   ToolingConfig   `json:"tooling"`
}

type AppConfig struct {
	Name        string `json:"name"`
	Environment string `json:"environment"`
}

type DatabaseConfig struct {
	Driver string `json:"driver"`
	Path   string `json:"path"`
}

type WorkspaceConfig struct {
	Root string `json:"root"`
}

type ChannelsConfig struct {
	Feishu FeishuConfig `json:"feishu"`
}

type FeishuConfig struct {
	Enabled            bool                           `json:"enabled"`
	DefaultAccount     string                         `json:"defaultAccount"`
	RenderMode         string                         `json:"renderMode"`
	Actions            FeishuActionsConfig            `json:"actions"`
	ProcessingAckEmoji string                         `json:"processingAckEmoji"`
	ProfileID          string                         `json:"profileId"`
	ConnectionMode     string                         `json:"connectionMode"`
	WebhookHost        string                         `json:"webhookHost"`
	WebhookPort        int                            `json:"webhookPort"`
	WebhookPath        string                         `json:"webhookPath"`
	APIBaseURL         string                         `json:"apiBaseUrl"`
	AppID              string                         `json:"appId"`
	AppSecret          string                         `json:"appSecret"`
	EncryptKey         string                         `json:"encryptKey"`
	VerificationToken  string                         `json:"verificationToken"`
	MaxBodyBytes       int64                          `json:"maxBodyBytes"`
	Accounts           map[string]FeishuAccountConfig `json:"accounts"`
}

type FeishuActionsConfig struct {
	ProcessingAck bool `json:"processingAck"`
	MessageSend   bool `json:"messageSend"`
	MessageUpdate bool `json:"messageUpdate"`
	MessageRecall bool `json:"messageRecall"`
	Reactions     bool `json:"reactions"`
	Pins          bool `json:"pins"`
	PostMessages  bool `json:"postMessages"`
	DocsRead      bool `json:"docsRead"`
}

type FeishuAccountConfig struct {
	Enabled            *bool                 `json:"enabled"`
	Name               string                `json:"name"`
	RenderMode         string                `json:"renderMode"`
	Actions            FeishuActionsOverride `json:"actions"`
	ProcessingAckEmoji string                `json:"processingAckEmoji"`
	ProfileID          string                `json:"profileId"`
	ConnectionMode     string                `json:"connectionMode"`
	WebhookHost        string                `json:"webhookHost"`
	WebhookPort        int                   `json:"webhookPort"`
	WebhookPath        string                `json:"webhookPath"`
	APIBaseURL         string                `json:"apiBaseUrl"`
	AppID              string                `json:"appId"`
	AppSecret          string                `json:"appSecret"`
	EncryptKey         string                `json:"encryptKey"`
	VerificationToken  string                `json:"verificationToken"`
	MaxBodyBytes       int64                 `json:"maxBodyBytes"`
}

type FeishuActionsOverride struct {
	ProcessingAck *bool `json:"processingAck"`
	MessageSend   *bool `json:"messageSend"`
	MessageUpdate *bool `json:"messageUpdate"`
	MessageRecall *bool `json:"messageRecall"`
	Reactions     *bool `json:"reactions"`
	Pins          *bool `json:"pins"`
	PostMessages  *bool `json:"postMessages"`
	DocsRead      *bool `json:"docsRead"`
}

type ModelConfig struct {
	Provider         string    `json:"provider"`
	API              model.API `json:"api"`
	BaseURL          string    `json:"baseUrl"`
	AuthHeader       bool      `json:"authHeader"`
	APIKey           string    `json:"apiKey"`
	ModelID          string    `json:"modelId"`
	MaxTokens        int       `json:"maxTokens"`
	AnthropicVersion string    `json:"anthropicVersion"`
	AnthropicBeta    string    `json:"anthropicBeta"`
}

type MemoryConfig struct {
	Provider    string            `json:"provider"`
	MemOS       MemOSConfig       `json:"memos"`
	AutoPromote AutoPromoteConfig `json:"autoPromote"`
}

type MemOSConfig struct {
	Enabled bool   `json:"enabled"`
	BaseURL string `json:"baseUrl"`
	APIKey  string `json:"apiKey"`
}

type AutoPromoteConfig struct {
	Enabled               bool                     `json:"enabled"`
	WeakPatternsEnabled   bool                     `json:"weakPatternsEnabled"`
	StrongPatternsEnabled bool                     `json:"strongPatternsEnabled"`
	RecentMessagesLimit   int                      `json:"recentMessagesLimit"`
	MinimumEvidence       int                      `json:"minimumEvidence"`
	PollIntervalSeconds   int                      `json:"pollIntervalSeconds"`
	Sources               AutoPromoteSourcesConfig `json:"sources"`
	Layers                AutoPromoteLayersConfig  `json:"layers"`
}

type AutoPromoteLayersConfig struct {
	Conversation  bool `json:"conversation"`
	Room          bool `json:"room"`
	PersonSummary bool `json:"personSummary"`
	PersonPrivate bool `json:"personPrivate"`
}

type AutoPromoteSourcesConfig struct {
	Transcript bool `json:"transcript"`
	Journals   bool `json:"journals"`
}

type ToolingConfig struct {
	LegacyJSONFallbackEnabled bool `json:"legacyJsonFallbackEnabled"`
	MaxIterations             int  `json:"maxIterations"`
}

func Default() Config {
	return Config{
		App: AppConfig{
			Name:        "goclaw",
			Environment: "development",
		},
		Database: DatabaseConfig{
			Driver: "sqlite3",
			Path:   "var/goclaw.db",
		},
		Workspace: WorkspaceConfig{
			Root: "",
		},
		Channels: ChannelsConfig{
			Feishu: FeishuConfig{
				Enabled:        false,
				DefaultAccount: "default",
				RenderMode:     "auto",
				Actions: FeishuActionsConfig{
					ProcessingAck: true,
					MessageSend:   true,
					MessageUpdate: true,
					MessageRecall: true,
					Reactions:     true,
					Pins:          true,
					PostMessages:  true,
					DocsRead:      true,
				},
				ProcessingAckEmoji: "THUMBSUP",
				ProfileID:          "main",
				ConnectionMode:     "webhook",
				WebhookHost:        "127.0.0.1",
				WebhookPort:        3000,
				WebhookPath:        "/feishu/events",
				APIBaseURL:         "https://open.feishu.cn/open-apis",
				MaxBodyBytes:       1 << 20,
			},
		},
		Model: ModelConfig{
			Provider:         "noop",
			API:              model.APIOpenAICompletions,
			BaseURL:          "",
			AuthHeader:       true,
			MaxTokens:        1024,
			AnthropicVersion: "2023-06-01",
		},
		Memory: MemoryConfig{
			Provider: "noop",
			MemOS: MemOSConfig{
				Enabled: false,
			},
			AutoPromote: AutoPromoteConfig{
				Enabled:               false,
				WeakPatternsEnabled:   false,
				StrongPatternsEnabled: false,
				RecentMessagesLimit:   128,
				MinimumEvidence:       2,
				PollIntervalSeconds:   30,
				Sources: AutoPromoteSourcesConfig{
					Transcript: true,
					Journals:   true,
				},
				Layers: AutoPromoteLayersConfig{
					Conversation:  true,
					Room:          true,
					PersonSummary: true,
					PersonPrivate: true,
				},
			},
		},
		Tooling: ToolingConfig{
			LegacyJSONFallbackEnabled: false,
			MaxIterations:             6,
		},
	}
}
