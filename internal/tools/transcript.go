package tools

import (
	"encoding/json"
	"fmt"
	"strings"
)

type StopReason string

const (
	StopReasonFinal   StopReason = "final"
	StopReasonToolUse StopReason = "tool_use"
)

type AssistantBlock interface {
	assistantBlock()
}

type TextBlock struct {
	Text string
}

func (TextBlock) assistantBlock() {}

type ToolCallBlock struct {
	ID   string
	Name string
	Args json.RawMessage
}

func (ToolCallBlock) assistantBlock() {}

type AssistantTurn struct {
	Blocks     []AssistantBlock
	StopReason StopReason
}

func (t AssistantTurn) Text() string {
	parts := make([]string, 0, len(t.Blocks))
	for _, block := range t.Blocks {
		textBlock, ok := block.(TextBlock)
		if !ok {
			continue
		}
		if strings.TrimSpace(textBlock.Text) == "" {
			continue
		}
		parts = append(parts, strings.TrimSpace(textBlock.Text))
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func (t AssistantTurn) ToolCalls() []ToolCallBlock {
	calls := make([]ToolCallBlock, 0, len(t.Blocks))
	for _, block := range t.Blocks {
		call, ok := block.(ToolCallBlock)
		if !ok {
			continue
		}
		calls = append(calls, call)
	}
	return calls
}

func ToolCallSummary(call ToolCallBlock) string {
	payload := struct {
		Type string          `json:"type"`
		Tool string          `json:"tool"`
		Args json.RawMessage `json:"args,omitempty"`
	}{
		Type: "tool_call",
		Tool: call.Name,
		Args: append(json.RawMessage(nil), call.Args...),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Sprintf(`{"type":"tool_call","tool":"%s"}`, call.Name)
	}
	return string(data)
}

type legacyEnvelope struct {
	Type    string          `json:"type"`
	Content string          `json:"content,omitempty"`
	Tool    string          `json:"tool,omitempty"`
	Args    json.RawMessage `json:"args,omitempty"`
}
