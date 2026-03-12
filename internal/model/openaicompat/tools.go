package openaicompat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"goclaw/internal/model"
)

const defaultToolIterations = 3

type responsesMessageItem struct {
	Type    string `json:"type"`
	Role    string `json:"role"`
	Content string `json:"content"`
}

type responsesFunctionTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Parameters  json.RawMessage `json:"parameters,omitempty"`
	} `json:"function"`
}

type responsesFunctionCallItem struct {
	Type      string `json:"type"`
	ID        string `json:"id,omitempty"`
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type responsesFunctionCallOutputItem struct {
	Type   string `json:"type"`
	CallID string `json:"call_id"`
	Output string `json:"output"`
}

type responsesOutputPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type responsesOutputItem struct {
	Type             string          `json:"type"`
	ID               string          `json:"id,omitempty"`
	CallID           string          `json:"call_id,omitempty"`
	Name             string          `json:"name,omitempty"`
	Arguments        string          `json:"arguments,omitempty"`
	Role             string          `json:"role,omitempty"`
	Content          json.RawMessage `json:"content,omitempty"`
	Text             string          `json:"text,omitempty"`
	EncryptedContent string          `json:"encrypted_content,omitempty"`
	Summary          string          `json:"summary,omitempty"`
}

type responsesResponse struct {
	OutputText string                `json:"output_text"`
	Output     []responsesOutputItem `json:"output"`
}

func (p *Provider) SupportsNativeTools() bool {
	switch p.api {
	case model.APIOpenAICompletions, model.APIOpenAIResponses, model.APIOpenAICodexResponses:
		return true
	default:
		return false
	}
}

func (p *Provider) GenerateWithTools(
	ctx context.Context,
	request model.Request,
	toolConfig model.ToolConfig,
	executor model.ToolExecutor,
) (string, error) {
	if !p.SupportsNativeTools() {
		return "", fmt.Errorf("native tools are unsupported for model api %q", p.api)
	}
	if len(toolConfig.Tools) == 0 || executor == nil {
		return p.Generate(ctx, request)
	}
	if err := p.validateConfigured(); err != nil {
		return "", err
	}
	switch p.api {
	case model.APIOpenAICompletions:
		return p.generateWithChatCompletionTools(ctx, request, toolConfig, executor)
	case model.APIOpenAIResponses, model.APIOpenAICodexResponses:
		return p.generateWithResponsesTools(ctx, request, toolConfig, executor)
	default:
		return "", fmt.Errorf("native tools are unsupported for model api %q", p.api)
	}
}

func (p *Provider) generateWithResponsesTools(
	ctx context.Context,
	request model.Request,
	toolConfig model.ToolConfig,
	executor model.ToolExecutor,
) (string, error) {
	input := toResponsesInput(request.Messages)
	maxIterations := toolConfig.MaxIterations
	if maxIterations <= 0 {
		maxIterations = defaultToolIterations
	}
	tools := toResponsesTools(toolConfig.Tools)

	for iteration := 0; iteration < maxIterations; iteration++ {
		response, err := p.createResponse(ctx, request.System, input, tools)
		if err != nil {
			return "", err
		}

		toolCalls, err := response.toolCalls()
		if err != nil {
			return "", err
		}
		if len(toolCalls) == 0 {
			return response.text(), nil
		}

		input = append(input, response.replayItems()...)
		toolOutputs, err := executeResponsesToolCalls(ctx, toolCalls, executor)
		if err != nil {
			return "", err
		}
		for _, toolOutput := range toolOutputs {
			input = append(input, toolOutput)
		}
	}

	return "", fmt.Errorf("openai responses tool loop exceeded %d iterations", maxIterations)
}

func (p *Provider) createResponse(
	ctx context.Context,
	system string,
	input []any,
	tools []responsesFunctionTool,
) (responsesResponse, error) {
	payload := struct {
		Model        string                  `json:"model"`
		Instructions string                  `json:"instructions,omitempty"`
		Input        []any                   `json:"input"`
		Tools        []responsesFunctionTool `json:"tools,omitempty"`
		ToolChoice   string                  `json:"tool_choice,omitempty"`
	}{
		Model:        p.modelID,
		Instructions: strings.TrimSpace(system),
		Input:        input,
		Tools:        tools,
	}
	if len(tools) > 0 {
		payload.ToolChoice = "auto"
	}

	responseBody, err := p.postJSON(ctx, "/responses", payload)
	if err != nil {
		return responsesResponse{}, err
	}

	var response responsesResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return responsesResponse{}, err
	}
	return response, nil
}

func toResponsesInput(messages []model.Message) []any {
	input := make([]any, 0, len(messages))
	for _, message := range messages {
		content := strings.TrimSpace(message.Content)
		if content == "" {
			continue
		}
		input = append(input, responsesMessageItem{
			Type:    "message",
			Role:    normalizeResponsesRole(message.Role),
			Content: content,
		})
	}
	return input
}

func toResponsesTools(tools []model.Tool) []responsesFunctionTool {
	if len(tools) == 0 {
		return nil
	}

	out := make([]responsesFunctionTool, 0, len(tools))
	for _, tool := range tools {
		if strings.TrimSpace(tool.Name) == "" || len(tool.InputSchema) == 0 {
			continue
		}
		record := responsesFunctionTool{Type: "function"}
		record.Function.Name = strings.TrimSpace(tool.Name)
		record.Function.Description = strings.TrimSpace(tool.Description)
		record.Function.Parameters = append(json.RawMessage(nil), tool.InputSchema...)
		out = append(out, record)
	}
	return out
}

func (r responsesResponse) text() string {
	if text := strings.TrimSpace(r.OutputText); text != "" {
		return text
	}

	parts := make([]string, 0, len(r.Output))
	for _, item := range r.Output {
		if item.Type != "message" {
			continue
		}
		text := strings.TrimSpace(item.text())
		if text == "" {
			continue
		}
		parts = append(parts, text)
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func (r responsesResponse) toolCalls() ([]model.ToolCall, error) {
	calls := make([]model.ToolCall, 0, len(r.Output))
	functionCallIndex := 0
	for _, item := range r.Output {
		if item.Type != "function_call" || strings.TrimSpace(item.Name) == "" {
			continue
		}
		args, err := parseToolArguments(item.Arguments)
		if err != nil {
			return nil, fmt.Errorf("parse tool arguments for %s: %w", item.Name, err)
		}
		calls = append(calls, model.ToolCall{
			ID:    repairResponsesCallID(item, functionCallIndex),
			Name:  strings.TrimSpace(item.Name),
			Input: args,
		})
		functionCallIndex++
	}
	return calls, nil
}

func (r responsesResponse) replayItems() []any {
	return repairResponsesReplayItems(r.Output)
}

func (i responsesOutputItem) text() string {
	if strings.TrimSpace(i.Text) != "" {
		return strings.TrimSpace(i.Text)
	}
	var contentParts []responsesOutputPart
	if len(i.Content) > 0 {
		_ = json.Unmarshal(i.Content, &contentParts)
	}
	parts := make([]string, 0, len(contentParts))
	for _, part := range contentParts {
		if part.Type != "output_text" && part.Type != "text" {
			continue
		}
		text := strings.TrimSpace(part.Text)
		if text == "" {
			continue
		}
		parts = append(parts, text)
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func (i responsesOutputItem) reasoningContent() string {
	if len(i.Content) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(i.Content, &text); err != nil {
		return ""
	}
	return strings.TrimSpace(text)
}

func executeResponsesToolCalls(
	ctx context.Context,
	calls []model.ToolCall,
	executor model.ToolExecutor,
) ([]responsesFunctionCallOutputItem, error) {
	results := make([]responsesFunctionCallOutputItem, 0, len(calls))
	for _, call := range calls {
		result, err := executor(ctx, call)
		if err != nil {
			return nil, err
		}
		results = append(results, responsesFunctionCallOutputItem{
			Type:   "function_call_output",
			CallID: authoritativeToolCallID(call, result),
			Output: result.Content,
		})
	}
	return results, nil
}

func authoritativeToolCallID(call model.ToolCall, result model.ToolResult) string {
	if callID := strings.TrimSpace(call.ID); callID != "" {
		return callID
	}
	return strings.TrimSpace(result.ToolCallID)
}

func parseToolArguments(value string) (json.RawMessage, error) {
	normalized := normalizeToolArgumentString(value)
	if !json.Valid([]byte(normalized)) {
		return nil, fmt.Errorf("invalid json: %q", value)
	}
	return json.RawMessage(normalized), nil
}

func normalizeToolArgumentString(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "{}"
	}
	return trimmed
}

func normalizeResponsesRole(role string) string {
	if strings.EqualFold(strings.TrimSpace(role), "assistant") {
		return "assistant"
	}
	return "user"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
