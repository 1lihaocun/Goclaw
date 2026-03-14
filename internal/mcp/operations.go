package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"goclaw/internal/config"
)

type PingSnapshot struct {
	Enabled bool                 `json:"enabled"`
	Servers []PingServerSnapshot `json:"servers"`
}

type PingServerSnapshot struct {
	Name      string `json:"name"`
	Status    string `json:"status"`
	ToolCount int    `json:"tool_count"`
	Error     string `json:"error,omitempty"`
}

type ToolCatalog struct {
	Enabled bool                 `json:"enabled"`
	Servers []PingServerSnapshot `json:"servers"`
	Tools   []ToolSnapshot       `json:"tools"`
}

type CallResult struct {
	Server       string         `json:"server"`
	Tool         string         `json:"tool"`
	ExposedName  string         `json:"exposed_name"`
	CapabilityID string         `json:"capability_id,omitempty"`
	Result       map[string]any `json:"result"`
}

func Ping(ctx context.Context, cfg config.MCPConfig, logger *slog.Logger) (PingSnapshot, error) {
	serverConfigs := resolveServerConfigs(cfg, logger)
	snapshot := PingSnapshot{
		Enabled: cfg.Enabled,
		Servers: make([]PingServerSnapshot, 0, len(serverConfigs)),
	}
	if len(serverConfigs) == 0 {
		return snapshot, nil
	}

	client := &Client{}
	defer func() {
		_ = client.Close()
	}()

	for _, server := range serverConfigs {
		serverView := PingServerSnapshot{Name: server.Name, Status: "ready"}
		tools, err := client.ListTools(ctx, server)
		if err != nil {
			serverView.Status = "error"
			serverView.Error = err.Error()
		} else {
			serverView.ToolCount = len(tools)
		}
		snapshot.Servers = append(snapshot.Servers, serverView)
	}
	return snapshot, nil
}

func Tools(ctx context.Context, cfg config.MCPConfig, logger *slog.Logger, serverName string) (ToolCatalog, error) {
	serverConfigs := resolveServerConfigs(cfg, logger)
	filter := normalizeToolName(serverName)
	catalog := ToolCatalog{
		Enabled: cfg.Enabled,
		Servers: make([]PingServerSnapshot, 0, len(serverConfigs)),
		Tools:   []ToolSnapshot{},
	}
	if len(serverConfigs) == 0 {
		return catalog, nil
	}

	client := &Client{}
	defer func() {
		_ = client.Close()
	}()

	matchedServer := filter == ""
	for _, server := range serverConfigs {
		if filter != "" && server.Name != filter {
			continue
		}
		matchedServer = true

		serverView := PingServerSnapshot{Name: server.Name, Status: "ready"}
		tools, err := client.ListTools(ctx, server)
		if err != nil {
			serverView.Status = "error"
			serverView.Error = err.Error()
			catalog.Servers = append(catalog.Servers, serverView)
			continue
		}
		serverView.ToolCount = len(tools)
		catalog.Servers = append(catalog.Servers, serverView)

		for _, tool := range tools {
			override := server.ToolOverrides[strings.TrimSpace(tool.Name)]
			exposedName, err := exposedToolName(server, tool, override)
			if err != nil {
				serverView.Status = "error"
				serverView.Error = err.Error()
				catalog.Servers[len(catalog.Servers)-1] = serverView
				break
			}
			catalog.Tools = append(catalog.Tools, ToolSnapshot{
				ServerName:    server.Name,
				Name:          tool.Name,
				ExposedName:   exposedName,
				Description:   tool.Description,
				SafetyClass:   string(resolveSafetyClass(server, tool)),
				CapabilityID:  capabilityID(server.Name, tool.Name),
				SchemaPresent: len(normalizeInputSchema(tool.InputSchema)) > 0,
			})
		}
	}
	if filter != "" && !matchedServer {
		return ToolCatalog{}, fmt.Errorf("mcp server %q not found", serverName)
	}
	return catalog, nil
}

func Call(
	ctx context.Context,
	cfg config.MCPConfig,
	logger *slog.Logger,
	serverName string,
	toolName string,
	args json.RawMessage,
) (CallResult, error) {
	server, err := resolveServer(cfg, logger, serverName)
	if err != nil {
		return CallResult{}, err
	}

	client := &Client{}
	defer func() {
		_ = client.Close()
	}()

	tools, err := client.ListTools(ctx, server)
	if err != nil {
		return CallResult{}, err
	}

	listed, exposedName, capability, err := resolveListedTool(server, tools, toolName)
	if err != nil {
		return CallResult{}, err
	}

	result, err := client.CallTool(ctx, server, listed.Name, args)
	if err != nil {
		return CallResult{}, err
	}
	return CallResult{
		Server:       server.Name,
		Tool:         listed.Name,
		ExposedName:  exposedName,
		CapabilityID: capability,
		Result:       result,
	}, nil
}

func resolveServer(cfg config.MCPConfig, logger *slog.Logger, serverName string) (serverConfig, error) {
	needle := normalizeToolName(serverName)
	if needle == "" {
		return serverConfig{}, fmt.Errorf("missing mcp server name")
	}
	for _, server := range resolveServerConfigs(cfg, logger) {
		if server.Name == needle {
			return server, nil
		}
	}
	return serverConfig{}, fmt.Errorf("mcp server %q not found", serverName)
}

func resolveListedTool(server serverConfig, tools []listedTool, toolName string) (listedTool, string, string, error) {
	needle := normalizeToolName(toolName)
	if needle == "" {
		return listedTool{}, "", "", fmt.Errorf("missing mcp tool name")
	}
	for _, tool := range tools {
		override := server.ToolOverrides[strings.TrimSpace(tool.Name)]
		exposedName, err := exposedToolName(server, tool, override)
		if err != nil {
			return listedTool{}, "", "", err
		}
		if normalizeToolName(tool.Name) == needle || normalizeToolName(exposedName) == needle {
			return tool, exposedName, capabilityID(server.Name, tool.Name), nil
		}
	}
	return listedTool{}, "", "", fmt.Errorf("mcp tool %q not found on server %q", toolName, server.Name)
}
