package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"goclaw/internal/domain"
	"goclaw/internal/model"
)

var (
	execToolSchema = json.RawMessage(`{
		"type":"object",
		"properties":{
			"program":{"type":"string"},
			"args":{"type":"array","items":{"type":"string"}},
			"workdir":{"type":"string"},
			"stdin":{"type":"string"},
			"timeout":{"type":"string"}
		},
		"required":["program"]
	}`)
	readToolSchema = json.RawMessage(`{
		"type":"object",
		"properties":{
			"path":{"type":"string"}
		},
		"required":["path"]
	}`)
	writeToolSchema = json.RawMessage(`{
		"type":"object",
		"properties":{
			"path":{"type":"string"},
			"content":{"type":"string"},
			"append":{"type":"boolean"},
			"mkdir":{"type":"boolean"}
		},
		"required":["path","content"]
	}`)
	fetchToolSchema = json.RawMessage(`{
		"type":"object",
		"properties":{
			"url":{"type":"string"},
			"timeout":{"type":"string"},
			"max_bytes":{"type":"integer"}
		},
		"required":["url"]
	}`)
)

type Definition struct {
	Name        string
	Description string
	InputSchema json.RawMessage
	Execute     func(context.Context, *LocalRuntime, domain.ProfileID, domain.RoomID, json.RawMessage) (any, error)
}

type ResultEnvelope struct {
	OK     bool   `json:"ok"`
	Tool   string `json:"tool"`
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

func DefaultCatalog() []Definition {
	return []Definition{
		{
			Name:        "exec",
			Description: "Run a local command in the room's permitted workspace.",
			InputSchema: append(json.RawMessage(nil), execToolSchema...),
			Execute: func(
				ctx context.Context,
				runtime *LocalRuntime,
				profileID domain.ProfileID,
				roomID domain.RoomID,
				raw json.RawMessage,
			) (any, error) {
				var args struct {
					Program string   `json:"program"`
					Args    []string `json:"args"`
					Workdir string   `json:"workdir"`
					Stdin   string   `json:"stdin"`
					Timeout string   `json:"timeout"`
				}
				if err := json.Unmarshal(raw, &args); err != nil {
					return nil, fmt.Errorf("parse exec args: %w", err)
				}
				timeout, err := parseOptionalDuration(args.Timeout)
				if err != nil {
					return nil, err
				}
				return runtime.RunCommand(ctx, profileID, roomID, CommandRequest{
					Program: args.Program,
					Args:    args.Args,
					Workdir: args.Workdir,
					Stdin:   args.Stdin,
					Timeout: timeout,
				})
			},
		},
		{
			Name:        "read",
			Description: "Read a local file if the room policy allows that path.",
			InputSchema: append(json.RawMessage(nil), readToolSchema...),
			Execute: func(
				ctx context.Context,
				runtime *LocalRuntime,
				profileID domain.ProfileID,
				roomID domain.RoomID,
				raw json.RawMessage,
			) (any, error) {
				var args struct {
					Path string `json:"path"`
				}
				if err := json.Unmarshal(raw, &args); err != nil {
					return nil, fmt.Errorf("parse read args: %w", err)
				}
				return runtime.ReadFile(ctx, profileID, roomID, args.Path)
			},
		},
		{
			Name:        "write",
			Description: "Write or append a local file if the room policy allows that path.",
			InputSchema: append(json.RawMessage(nil), writeToolSchema...),
			Execute: func(
				ctx context.Context,
				runtime *LocalRuntime,
				profileID domain.ProfileID,
				roomID domain.RoomID,
				raw json.RawMessage,
			) (any, error) {
				var args struct {
					Path    string `json:"path"`
					Content string `json:"content"`
					Append  bool   `json:"append"`
					Mkdir   bool   `json:"mkdir"`
				}
				if err := json.Unmarshal(raw, &args); err != nil {
					return nil, fmt.Errorf("parse write args: %w", err)
				}
				return runtime.WriteFile(ctx, profileID, roomID, WriteFileRequest{
					Path:       args.Path,
					Content:    args.Content,
					Append:     args.Append,
					CreateDirs: args.Mkdir,
				})
			},
		},
		{
			Name:        "fetch",
			Description: "Fetch an HTTP or HTTPS URL if the room policy allows that host.",
			InputSchema: append(json.RawMessage(nil), fetchToolSchema...),
			Execute: func(
				ctx context.Context,
				runtime *LocalRuntime,
				profileID domain.ProfileID,
				roomID domain.RoomID,
				raw json.RawMessage,
			) (any, error) {
				var args struct {
					URL      string `json:"url"`
					Timeout  string `json:"timeout"`
					MaxBytes int64  `json:"max_bytes"`
				}
				if err := json.Unmarshal(raw, &args); err != nil {
					return nil, fmt.Errorf("parse fetch args: %w", err)
				}
				timeout, err := parseOptionalDuration(args.Timeout)
				if err != nil {
					return nil, err
				}
				return runtime.FetchURL(ctx, profileID, roomID, FetchRequest{
					URL:      args.URL,
					Timeout:  timeout,
					MaxBytes: args.MaxBytes,
				})
			},
		},
	}
}

func VisibleDefinitions(policy domain.ToolPermissionPolicy) []Definition {
	visible := make([]Definition, 0, len(DefaultCatalog()))
	allowCommands := policy.CommandsMode == domain.ToolPermissionAllowAll ||
		len(policy.AllowedCommands) > 0
	allowPaths := policy.PathsMode == domain.ToolPermissionAllowAll ||
		len(policy.AllowedPaths) > 0
	allowNetwork := policy.NetworkMode == domain.ToolPermissionAllowAll ||
		len(policy.AllowedHosts) > 0

	for _, definition := range DefaultCatalog() {
		switch definition.Name {
		case "exec":
			if allowCommands {
				visible = append(visible, definition)
			}
		case "read", "write":
			if allowPaths {
				visible = append(visible, definition)
			}
		case "fetch":
			if allowNetwork {
				visible = append(visible, definition)
			}
		}
	}
	return visible
}

func BuildModelDefinitions(policy domain.ToolPermissionPolicy) []model.Tool {
	visible := VisibleDefinitions(policy)
	out := make([]model.Tool, 0, len(visible))
	for _, definition := range visible {
		out = append(out, model.Tool{
			Name:        definition.Name,
			Description: definition.Description,
			InputSchema: append(json.RawMessage(nil), definition.InputSchema...),
		})
	}
	return out
}

func LookupDefinition(name string) (Definition, bool) {
	normalized := strings.TrimSpace(name)
	for _, definition := range DefaultCatalog() {
		if definition.Name == normalized {
			return definition, true
		}
	}
	return Definition{}, false
}

func ExecuteCall(
	ctx context.Context,
	runtime *LocalRuntime,
	profileID domain.ProfileID,
	roomID domain.RoomID,
	call ToolCallBlock,
) (string, error) {
	if runtime == nil {
		return "", errors.New("tools: local runtime is not configured")
	}

	definition, ok := LookupDefinition(call.Name)
	if !ok {
		return MarshalResult(ResultEnvelope{
			OK:    false,
			Tool:  call.Name,
			Error: "unknown tool",
		})
	}

	result, err := definition.Execute(ctx, runtime, profileID, roomID, append(json.RawMessage(nil), call.Args...))
	if err != nil {
		return MarshalResult(ResultEnvelope{
			OK:    false,
			Tool:  definition.Name,
			Error: err.Error(),
		})
	}
	return MarshalResult(ResultEnvelope{
		OK:     true,
		Tool:   definition.Name,
		Result: result,
	})
}

func ResultIsError(content string) bool {
	var envelope struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &envelope); err != nil {
		return false
	}
	return !envelope.OK
}

func MarshalResult(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func parseOptionalDuration(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("parse duration %q: %w", value, err)
	}
	return duration, nil
}
