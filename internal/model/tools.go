package model

import (
	"context"
	"encoding/json"
)

type Tool struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

type ToolConfig struct {
	Tools         []Tool
	MaxIterations int
}

type ToolCall struct {
	ID    string
	Name  string
	Input json.RawMessage
}

type ToolResult struct {
	ToolCallID string
	ToolName   string
	Content    string
	IsError    bool
}

type ToolExecutor func(context.Context, ToolCall) (ToolResult, error)

type NativeToolProvider interface {
	GenerateWithTools(context.Context, Request, ToolConfig, ToolExecutor) (string, error)
}

type NativeToolSupportReporter interface {
	SupportsNativeTools() bool
}

func SupportsNativeTools(provider Provider) bool {
	_, ok := NativeToolProviderFor(provider)
	return ok
}

func NativeToolProviderFor(provider Provider) (NativeToolProvider, bool) {
	native, ok := provider.(NativeToolProvider)
	if !ok {
		return nil, false
	}
	reporter, ok := provider.(NativeToolSupportReporter)
	if !ok {
		return native, true
	}
	if !reporter.SupportsNativeTools() {
		return nil, false
	}
	return native, true
}
