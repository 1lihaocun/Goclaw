package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"goclaw/internal/agenttools"
	"goclaw/internal/config"
	"goclaw/internal/domain"
)

type Provider struct {
	entries []agenttools.Definition
	client  ToolClient
	logger  *slog.Logger
}

func NewProvider(
	ctx context.Context,
	cfg config.MCPConfig,
	logger *slog.Logger,
) (agenttools.Provider, error) {
	return newProvider(ctx, cfg, logger, &Client{})
}

func newProvider(
	ctx context.Context,
	cfg config.MCPConfig,
	logger *slog.Logger,
	client ToolClient,
) (agenttools.Provider, error) {
	serverConfigs := resolveServerConfigs(cfg, logger)
	if len(serverConfigs) == 0 {
		return nil, nil
	}
	if client == nil {
		client = &Client{}
	}

	definitions := make([]agenttools.Definition, 0)
	seenNames := make(map[string]struct{})
	var loadErr error

	for _, server := range serverConfigs {
		tools, err := client.ListTools(ctx, server)
		if err != nil {
			loadErr = errors.Join(loadErr, fmt.Errorf("list mcp tools for %s: %w", server.Name, err))
			if logger != nil {
				logger.Warn("mcp: failed to list tools for server", "server", server.Name, "error", err)
			}
			continue
		}
		for _, tool := range tools {
			if strings.TrimSpace(tool.Name) == "" {
				continue
			}
			override := server.ToolOverrides[strings.TrimSpace(tool.Name)]
			exposedName, err := exposedToolName(server, tool, override)
			if err != nil {
				return nil, err
			}
			if _, ok := seenNames[exposedName]; ok {
				return nil, fmt.Errorf("duplicate mcp tool name %q", exposedName)
			}
			seenNames[exposedName] = struct{}{}

			serverCopy := server
			toolCopy := tool
			exposedNameCopy := exposedName
			capability := capabilityID(server.Name, tool.Name)
			definitions = append(definitions, agenttools.Definition{
				Name:               exposedNameCopy,
				Description:        firstNonEmpty(tool.Description, fmt.Sprintf("Call MCP tool %s on server %s.", tool.Name, server.Name)),
				InputSchema:        normalizeInputSchema(tool.InputSchema),
				Source:             agenttools.SourceMCP,
				Provider:           providerName(server.Name),
				ProfileBindingMode: agenttools.ProfileBindingCurrentSession,
				SafetyClass:        resolveSafetyClass(server, tool),
				CapabilityID:       capability,
				Execute: func(ctx context.Context, _ agenttools.ExecutionContext, args json.RawMessage) (any, error) {
					result, err := client.CallTool(ctx, serverCopy, toolCopy.Name, args)
					if err != nil {
						return nil, &agenttools.ExecutionError{
							Kind:       agenttools.ErrorProvider,
							Provider:   providerName(serverCopy.Name),
							Tool:       exposedNameCopy,
							Message:    err.Error(),
							Retryable:  true,
							Capability: capability,
						}
					}
					if isErrorResult(result) {
						return nil, &agenttools.ExecutionError{
							Kind:       agenttools.ErrorProvider,
							Provider:   providerName(serverCopy.Name),
							Tool:       exposedNameCopy,
							Message:    summarizeResultError(result),
							Details:    result,
							Capability: capability,
						}
					}
					return result, nil
				},
			})
		}
	}

	if len(definitions) == 0 {
		return nil, loadErr
	}
	return &Provider{entries: definitions, client: client, logger: logger}, nil
}

func (p *Provider) VisibleTools(
	_ agenttools.SessionContext,
	policy domain.ToolPermissionPolicy,
) []agenttools.Definition {
	if p == nil {
		return nil
	}
	out := make([]agenttools.Definition, 0, len(p.entries))
	for _, definition := range p.entries {
		if !agenttools.AllowsMCPTool(policy, definition) {
			continue
		}
		out = append(out, definition)
	}
	return out
}

func (p *Provider) CapabilitySummary(
	_ context.Context,
	_ agenttools.SessionContext,
	_ domain.ToolPermissionPolicy,
) (agenttools.CapabilitySummary, bool, error) {
	return agenttools.CapabilitySummary{}, false, nil
}

func (p *Provider) Close() error {
	if p == nil || p.client == nil {
		return nil
	}
	closer, ok := p.client.(io.Closer)
	if !ok {
		return nil
	}
	return closer.Close()
}

func isErrorResult(result map[string]any) bool {
	value, ok := result["isError"]
	if !ok {
		return false
	}
	flag, ok := value.(bool)
	return ok && flag
}

func summarizeResultError(result map[string]any) string {
	content, ok := result["content"].([]any)
	if ok {
		for _, item := range content {
			part, ok := item.(map[string]any)
			if !ok {
				continue
			}
			text, _ := part["text"].(string)
			if strings.TrimSpace(text) != "" {
				return strings.TrimSpace(text)
			}
		}
	}
	return "mcp server reported tool error"
}
