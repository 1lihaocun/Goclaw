package mcp

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"

	"goclaw/internal/agenttools"
	"goclaw/internal/config"
	"goclaw/internal/domain"
)

var toolNameSanitizer = regexp.MustCompile(`[^a-z0-9_]+`)

var emptyInputSchema = json.RawMessage(`{"type":"object","properties":{}}`)

type listedTool struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

type toolOverride struct {
	Alias       string
	SafetyClass agenttools.SafetyClass
}

type serverConfig struct {
	Name                string
	Command             string
	Args                []string
	Env                 map[string]string
	SafetyClass         agenttools.SafetyClass
	ToolPrefix          string
	HandshakeTimeoutSec int
	CallTimeoutSec      int
	ToolOverrides       map[string]toolOverride
}

func resolveServerConfigs(cfg config.MCPConfig, logger *slog.Logger) []serverConfig {
	if !cfg.Enabled || len(cfg.Servers) == 0 {
		return nil
	}
	serverNames := make([]string, 0, len(cfg.Servers))
	for name := range cfg.Servers {
		serverNames = append(serverNames, name)
	}
	slices.Sort(serverNames)

	out := make([]serverConfig, 0, len(serverNames))
	for _, name := range serverNames {
		serverCfg := cfg.Servers[name]
		if serverCfg.Enabled != nil && !*serverCfg.Enabled {
			continue
		}
		command := strings.TrimSpace(serverCfg.Command)
		if command == "" {
			if logger != nil {
				logger.Warn("mcp: skipping server with empty command", "server", name)
			}
			continue
		}
		out = append(out, serverConfig{
			Name:                normalizeToolName(name),
			Command:             command,
			Args:                append([]string(nil), serverCfg.Args...),
			Env:                 copyStringMap(serverCfg.Env),
			SafetyClass:         parseSafetyClass(serverCfg.SafetyClass),
			ToolPrefix:          normalizeToolName(serverCfg.ToolPrefix),
			HandshakeTimeoutSec: serverCfg.HandshakeTimeoutSec,
			CallTimeoutSec:      serverCfg.CallTimeoutSec,
			ToolOverrides:       resolveToolOverrides(serverCfg.Tools),
		})
	}
	return out
}

func resolveToolOverrides(raw map[string]config.MCPToolConfig) map[string]toolOverride {
	if len(raw) == 0 {
		return nil
	}
	out := make(map[string]toolOverride, len(raw))
	for name, override := range raw {
		out[strings.TrimSpace(name)] = toolOverride{
			Alias:       normalizeToolName(override.Alias),
			SafetyClass: parseSafetyClass(override.SafetyClass),
		}
	}
	return out
}

func parseSafetyClass(value string) agenttools.SafetyClass {
	switch agenttools.SafetyClass(strings.TrimSpace(value)) {
	case agenttools.SafetyIntrospection, agenttools.SafetyReadOnly, agenttools.SafetyMutating, agenttools.SafetySensitive:
		return agenttools.SafetyClass(strings.TrimSpace(value))
	default:
		return ""
	}
}

func normalizeToolName(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	normalized = toolNameSanitizer.ReplaceAllString(normalized, "_")
	normalized = strings.Trim(normalized, "_")
	return normalized
}

func exposedToolName(server serverConfig, tool listedTool, override toolOverride) (string, error) {
	if alias := strings.TrimSpace(override.Alias); alias != "" {
		return alias, nil
	}
	toolName := normalizeToolName(tool.Name)
	if toolName == "" {
		return "", fmt.Errorf("mcp server %q returned tool with empty name", server.Name)
	}
	prefix := firstNonEmpty(server.ToolPrefix, server.Name)
	name := normalizeToolName("mcp_" + prefix + "_" + toolName)
	if name == "" {
		return "", fmt.Errorf("cannot normalize mcp tool name for %s/%s", server.Name, tool.Name)
	}
	return name, nil
}

func resolveSafetyClass(server serverConfig, tool listedTool) agenttools.SafetyClass {
	if override, ok := server.ToolOverrides[strings.TrimSpace(tool.Name)]; ok && override.SafetyClass != "" {
		return override.SafetyClass
	}
	if server.SafetyClass != "" {
		return server.SafetyClass
	}
	return inferSafetyClass(tool.Name)
}

func normalizeInputSchema(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || !json.Valid(raw) {
		return append(json.RawMessage(nil), emptyInputSchema...)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return append(json.RawMessage(nil), emptyInputSchema...)
	}
	if payload == nil {
		return append(json.RawMessage(nil), emptyInputSchema...)
	}
	if _, ok := payload["type"]; !ok {
		payload["type"] = "object"
	}
	if _, ok := payload["properties"]; !ok {
		payload["properties"] = map[string]any{}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return append(json.RawMessage(nil), emptyInputSchema...)
	}
	return data
}

func capabilityID(serverName, toolName string) string {
	serverPart := normalizeToolName(serverName)
	toolPart := normalizeToolName(toolName)
	return strings.Trim(strings.Join([]string{"mcp", serverPart, toolPart}, "."), ".")
}

func inferSafetyClass(name string) agenttools.SafetyClass {
	tokens := splitNameTokens(name)
	for _, token := range tokens {
		switch token {
		case "exec", "execute", "shell", "command", "run", "script":
			return agenttools.SafetySensitive
		}
	}
	for _, token := range tokens {
		switch token {
		case "delete", "remove", "update", "write", "set", "create", "send", "post", "append", "reply", "comment", "pin", "react":
			return agenttools.SafetyMutating
		}
	}
	for _, token := range tokens {
		switch token {
		case "status", "whoami", "health", "capabilities", "version", "info", "me", "scope":
			return agenttools.SafetyIntrospection
		}
	}
	return agenttools.SafetyReadOnly
}

func splitNameTokens(value string) []string {
	normalized := toolNameSanitizer.ReplaceAllString(strings.ToLower(strings.TrimSpace(value)), "_")
	parts := strings.Split(normalized, "_")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

func copyStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		return strings.TrimSpace(value)
	}
	return ""
}

func providerName(serverName string) domain.Provider {
	return domain.Provider(strings.TrimSpace(serverName))
}
