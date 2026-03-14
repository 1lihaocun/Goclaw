package config

import "goclaw/internal/model"

type Config struct {
	App       AppConfig       `json:"app"`
	Database  DatabaseConfig  `json:"database"`
	Workspace WorkspaceConfig `json:"workspace"`
	Gateway   GatewayConfig   `json:"gateway"`
	Channels  ChannelsConfig  `json:"channels"`
	Model     ModelConfig     `json:"model"`
	Memory    MemoryConfig    `json:"memory"`
	Tooling   ToolingConfig   `json:"tooling"`
	MCP       MCPConfig       `json:"mcp"`
	Skills    SkillsConfig    `json:"skills"`

	SourcePath string `json:"-"`
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

type GatewayConfig struct {
	Restart GatewayRestartConfig `json:"restart"`
	Control GatewayControlConfig `json:"control"`
}

type GatewayRestartConfig struct {
	Enabled               bool `json:"enabled"`
	MaxAttempts           int  `json:"maxAttempts"`
	InitialBackoffSeconds int  `json:"initialBackoffSeconds"`
	MaxBackoffSeconds     int  `json:"maxBackoffSeconds"`
}

type GatewayControlConfig struct {
	Enabled bool   `json:"enabled"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
}

type ChannelsConfig struct {
	Feishu FeishuConfig `json:"feishu"`
}

type FeishuConfig struct {
	Enabled                bool                           `json:"enabled"`
	DefaultAccount         string                         `json:"defaultAccount"`
	RenderMode             string                         `json:"renderMode"`
	StreamingToolSummaries string                         `json:"streamingToolSummaries"`
	Actions                FeishuActionsConfig            `json:"actions"`
	ProcessingAckEmoji     string                         `json:"processingAckEmoji"`
	ProfileID              string                         `json:"profileId"`
	ConnectionMode         string                         `json:"connectionMode"`
	WebhookHost            string                         `json:"webhookHost"`
	WebhookPort            int                            `json:"webhookPort"`
	WebhookPath            string                         `json:"webhookPath"`
	APIBaseURL             string                         `json:"apiBaseUrl"`
	AppID                  string                         `json:"appId"`
	AppSecret              string                         `json:"appSecret"`
	EncryptKey             string                         `json:"encryptKey"`
	VerificationToken      string                         `json:"verificationToken"`
	MaxBodyBytes           int64                          `json:"maxBodyBytes"`
	Accounts               map[string]FeishuAccountConfig `json:"accounts"`
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
	Enabled                *bool                 `json:"enabled"`
	Name                   string                `json:"name"`
	RenderMode             string                `json:"renderMode"`
	StreamingToolSummaries string                `json:"streamingToolSummaries"`
	Actions                FeishuActionsOverride `json:"actions"`
	ProcessingAckEmoji     string                `json:"processingAckEmoji"`
	ProfileID              string                `json:"profileId"`
	ConnectionMode         string                `json:"connectionMode"`
	WebhookHost            string                `json:"webhookHost"`
	WebhookPort            int                   `json:"webhookPort"`
	WebhookPath            string                `json:"webhookPath"`
	APIBaseURL             string                `json:"apiBaseUrl"`
	AppID                  string                `json:"appId"`
	AppSecret              string                `json:"appSecret"`
	EncryptKey             string                `json:"encryptKey"`
	VerificationToken      string                `json:"verificationToken"`
	MaxBodyBytes           int64                 `json:"maxBodyBytes"`
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

type MCPConfig struct {
	Enabled bool                       `json:"enabled"`
	Servers map[string]MCPServerConfig `json:"servers"`
}

type MCPServerConfig struct {
	Enabled             *bool                    `json:"enabled"`
	Command             string                   `json:"command"`
	Args                []string                 `json:"args"`
	Env                 map[string]string        `json:"env"`
	SafetyClass         string                   `json:"safetyClass"`
	ToolPrefix          string                   `json:"toolPrefix"`
	HandshakeTimeoutSec int                      `json:"handshakeTimeoutSec"`
	CallTimeoutSec      int                      `json:"callTimeoutSec"`
	Tools               map[string]MCPToolConfig `json:"tools"`
}

type MCPToolConfig struct {
	Alias       string `json:"alias"`
	SafetyClass string `json:"safetyClass"`
}

type SkillsConfig struct {
	Enabled          bool   `json:"enabled"`
	Root             string `json:"root"`
	MaxEntries       int    `json:"maxEntries"`
	MaxInline        int    `json:"maxInline"`
	MaxBytesPerSkill int    `json:"maxBytesPerSkill"`
	MaxPromptBytes   int    `json:"maxPromptBytes"`
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
		Gateway: GatewayConfig{
			Restart: GatewayRestartConfig{
				Enabled:               false,
				MaxAttempts:           3,
				InitialBackoffSeconds: 1,
				MaxBackoffSeconds:     30,
			},
			Control: GatewayControlConfig{
				Enabled: true,
				Host:    "127.0.0.1",
				Port:    3100,
			},
		},
		Channels: ChannelsConfig{
			Feishu: FeishuConfig{
				Enabled:                false,
				DefaultAccount:         "default",
				RenderMode:             "auto",
				StreamingToolSummaries: "off",
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
		MCP: MCPConfig{
			Enabled: false,
		},
		Skills: SkillsConfig{
			Enabled:          true,
			MaxEntries:       32,
			MaxInline:        4,
			MaxBytesPerSkill: 4000,
			MaxPromptBytes:   16000,
		},
	}
}
