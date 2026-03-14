package mcp

import (
	"context"
	"log/slog"
	"strings"

	"goclaw/internal/config"
)

type Snapshot struct {
	Enabled bool             `json:"enabled"`
	Servers []ServerSnapshot `json:"servers"`
}

type ServerSnapshot struct {
	Name        string         `json:"name"`
	Command     string         `json:"command"`
	Args        []string       `json:"args,omitempty"`
	ToolPrefix  string         `json:"tool_prefix,omitempty"`
	SafetyClass string         `json:"safety_class,omitempty"`
	Status      string         `json:"status"`
	Error       string         `json:"error,omitempty"`
	Tools       []ToolSnapshot `json:"tools,omitempty"`
}

type ToolSnapshot struct {
	ServerName    string `json:"server_name"`
	Name          string `json:"name"`
	ExposedName   string `json:"exposed_name"`
	Description   string `json:"description,omitempty"`
	SafetyClass   string `json:"safety_class,omitempty"`
	CapabilityID  string `json:"capability_id,omitempty"`
	SchemaPresent bool   `json:"schema_present"`
}

func Inspect(ctx context.Context, cfg config.MCPConfig, logger *slog.Logger) (Snapshot, error) {
	snapshot := Snapshot{Enabled: cfg.Enabled}
	serverConfigs := resolveServerConfigs(cfg, logger)
	if len(serverConfigs) == 0 {
		return snapshot, nil
	}

	client := &Client{}
	defer func() {
		_ = client.Close()
	}()

	servers := make([]ServerSnapshot, 0, len(serverConfigs))
	for _, server := range serverConfigs {
		serverView := ServerSnapshot{
			Name:        server.Name,
			Command:     server.Command,
			Args:        append([]string(nil), server.Args...),
			ToolPrefix:  strings.TrimSpace(server.ToolPrefix),
			SafetyClass: string(server.SafetyClass),
			Status:      "ready",
		}
		tools, err := client.ListTools(ctx, server)
		if err != nil {
			serverView.Status = "error"
			serverView.Error = err.Error()
			servers = append(servers, serverView)
			continue
		}
		serverView.Tools = make([]ToolSnapshot, 0, len(tools))
		for _, tool := range tools {
			override := server.ToolOverrides[strings.TrimSpace(tool.Name)]
			exposedName, err := exposedToolName(server, tool, override)
			if err != nil {
				serverView.Status = "error"
				serverView.Error = err.Error()
				serverView.Tools = nil
				break
			}
			serverView.Tools = append(serverView.Tools, ToolSnapshot{
				ServerName:    server.Name,
				Name:          tool.Name,
				ExposedName:   exposedName,
				Description:   tool.Description,
				SafetyClass:   string(resolveSafetyClass(server, tool)),
				CapabilityID:  capabilityID(server.Name, tool.Name),
				SchemaPresent: len(normalizeInputSchema(tool.InputSchema)) > 0,
			})
		}
		servers = append(servers, serverView)
	}
	snapshot.Servers = servers
	return snapshot, nil
}
