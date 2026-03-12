package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"goclaw/internal/agenttools"
	channelcore "goclaw/internal/channels"
	feishuchannel "goclaw/internal/channels/feishu"
	"goclaw/internal/config"
	"goclaw/internal/memory"
	memorymemos "goclaw/internal/memory/memos"
	"goclaw/internal/memory/noop"
	"goclaw/internal/model"
	modelanthropic "goclaw/internal/model/anthropic"
	modelecho "goclaw/internal/model/echo"
	modelnoop "goclaw/internal/model/noop"
	modelopenaicompat "goclaw/internal/model/openaicompat"
	sqlitestore "goclaw/internal/store/sqlite"
	toolruntime "goclaw/internal/tools"
	workspacectx "goclaw/internal/workspace"
)

const shutdownTimeout = 5 * time.Second

type App struct {
	Config     config.Config
	Store      *sqlitestore.Store
	Memory     memory.Provider
	Model      model.Provider
	Repos      sqlitestore.Repositories
	Channels   *channelcore.Registry
	channelErr error
}

func New(cfg config.Config) *App {
	store := sqlitestore.NewWithDriver(cfg.Database.Driver, cfg.Database.Path)
	registry, registryErr := channelcore.NewRegistry(
		feishuchannel.New(cfg.Channels.Feishu, slog.Default()),
	)
	return &App{
		Config:     cfg,
		Store:      store,
		Memory:     resolveMemoryProvider(cfg),
		Model:      resolveModelProvider(cfg),
		Repos:      sqlitestore.NewRepositories(store.DBTX()),
		Channels:   registry,
		channelErr: registryErr,
	}
}

func (a *App) Summary(version string) string {
	return fmt.Sprintf(
		"GoClaw %s\nprofile model: single-profile\nstore: %s (%s)\nmemory provider: %s\nmodel provider: %s",
		version,
		a.Config.Database.Driver,
		a.Store.Path,
		a.Memory.Name(),
		a.Model.Name(),
	)
}

func (a *App) Initialize(ctx context.Context) error {
	if a.channelErr != nil {
		return a.channelErr
	}
	if err := a.Store.Open(); err != nil {
		return err
	}
	if err := a.Store.Ping(ctx); err != nil {
		return err
	}
	if err := a.Store.Migrate(ctx); err != nil {
		return err
	}
	a.Repos = sqlitestore.NewRepositories(a.Store.DBTX())
	return nil
}

func (a *App) NewBuildRuntimeParams(logger *slog.Logger) channelcore.BuildRuntimeParams {
	if logger == nil {
		logger = slog.Default()
	}

	localRuntime := toolruntime.NewLocalRuntime(a.Repos.ToolPermissions, nil)
	agentToolRegistry, err := a.newAgentToolRegistry(logger, localRuntime)
	if err != nil {
		logger.Error("runtime: failed to build agent tool registry", "error", err)
	}

	replyService := NewReplyService(
		a.Repos,
		NewPromptBuilder(a.Repos, a.Memory, workspacectx.NewLoader(a.Config.Workspace.Root)),
		a.Model,
		a.Channels,
	)
	replyService.Logger = logger
	replyService.LegacyToolFallbackEnabled = a.Config.Tooling.LegacyJSONFallbackEnabled
	replyService.ToolMaxIterations = a.Config.Tooling.MaxIterations
	replyService.Tools = localRuntime
	replyService.AgentTools = agentToolRegistry

	eventService := NewChannelEventService(a.Repos, logger)
	executor := newSerializedRoomExecutor()

	return channelcore.BuildRuntimeParams{
		Logger:        logger,
		Ingestor:      newSerializedInboundIngestor(executor, replyService),
		EventObserver: newSerializedChannelEventObserver(executor, eventService),
	}
}

func (a *App) newAgentToolRegistry(
	logger *slog.Logger,
	localRuntime *toolruntime.LocalRuntime,
) (*agenttools.Registry, error) {
	providers := []agenttools.Provider{
		toolruntime.NewLocalToolProvider(localRuntime),
	}
	channelProviders, err := a.Channels.AgentToolProviders(channelcore.ToolBuildParams{
		Logger: logger,
		Repos:  a.Repos,
		Now:    time.Now,
	})
	if err != nil {
		return nil, err
	}
	providers = append(providers, channelProviders...)
	return agenttools.NewRegistry(providers...), nil
}

func resolveMemoryProvider(cfg config.Config) memory.Provider {
	if cfg.Memory.Provider == "memos" && cfg.Memory.MemOS.Enabled {
		return memorymemos.New(memorymemos.Config{
			BaseURL: cfg.Memory.MemOS.BaseURL,
			APIKey:  cfg.Memory.MemOS.APIKey,
		})
	}
	return noop.New()
}

func resolveModelProvider(cfg config.Config) model.Provider {
	switch cfg.Model.Provider {
	case "echo":
		return modelecho.New()
	case "anthropic":
		return modelanthropic.New(modelanthropic.Config{
			BaseURL:    cfg.Model.BaseURL,
			APIKey:     cfg.Model.APIKey,
			ModelID:    cfg.Model.ModelID,
			MaxTokens:  cfg.Model.MaxTokens,
			Version:    cfg.Model.AnthropicVersion,
			BetaHeader: cfg.Model.AnthropicBeta,
		})
	case "openai-compatible":
		return modelopenaicompat.New(modelopenaicompat.Config{
			API:               cfg.Model.API,
			BaseURL:           cfg.Model.BaseURL,
			APIKey:            cfg.Model.APIKey,
			ModelID:           cfg.Model.ModelID,
			DisableAuthHeader: !cfg.Model.AuthHeader,
		})
	default:
		return modelnoop.New()
	}
}
