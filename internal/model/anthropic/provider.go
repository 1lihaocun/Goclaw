package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"goclaw/internal/model"
)

const (
	defaultBaseURL        = "https://api.anthropic.com"
	defaultVersion        = "2023-06-01"
	defaultMaxTokens      = 1024
	defaultToolIterations = 3
)

type Config struct {
	BaseURL    string
	APIKey     string
	ModelID    string
	MaxTokens  int
	Version    string
	BetaHeader string
	HTTPClient *http.Client
}

type Provider struct {
	baseURL    string
	apiKey     string
	modelID    string
	maxTokens  int
	version    string
	betaHeader string
	httpClient *http.Client
}

type payloadMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthropicContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

type anthropicMessageResponse struct {
	Content []anthropicContentBlock `json:"content"`
}

func New(cfg Config) *Provider {
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}

	maxTokens := cfg.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}

	version := strings.TrimSpace(cfg.Version)
	if version == "" {
		version = defaultVersion
	}

	return &Provider{
		baseURL:    normalizeBaseURL(cfg.BaseURL),
		apiKey:     strings.TrimSpace(cfg.APIKey),
		modelID:    strings.TrimSpace(cfg.ModelID),
		maxTokens:  maxTokens,
		version:    version,
		betaHeader: normalizeHeaderList(cfg.BetaHeader),
		httpClient: httpClient,
	}
}

func (p *Provider) Name() string {
	return "anthropic"
}

func (p *Provider) Generate(ctx context.Context, request model.Request) (string, error) {
	return p.generate(ctx, request, model.ToolConfig{}, nil)
}

func (p *Provider) GenerateWithTools(
	ctx context.Context,
	request model.Request,
	toolConfig model.ToolConfig,
	executor model.ToolExecutor,
) (string, error) {
	return p.generate(ctx, request, toolConfig, executor)
}

func (p *Provider) generate(
	ctx context.Context,
	request model.Request,
	toolConfig model.ToolConfig,
	executor model.ToolExecutor,
) (string, error) {
	if p.apiKey == "" {
		return "", fmt.Errorf("model api key is not configured")
	}
	if p.modelID == "" {
		return "", fmt.Errorf("model id is not configured")
	}

	messages := normalizeMessages(request.Messages)
	if len(messages) == 0 {
		return "", fmt.Errorf("anthropic request has no user messages")
	}

	maxIterations := toolConfig.MaxIterations
	if maxIterations <= 0 {
		maxIterations = defaultToolIterations
	}

	for iteration := 0; iteration < maxIterations; iteration++ {
		response, err := p.createMessage(ctx, anthropicMessageRequest{
			System:   strings.TrimSpace(request.System),
			Messages: messages,
			Tools:    toAnthropicTools(toolConfig.Tools),
		})
		if err != nil {
			return "", err
		}

		toolCalls := response.toolCalls()
		if len(toolCalls) == 0 || executor == nil || len(toolConfig.Tools) == 0 {
			return response.text(), nil
		}

		// Anthropic expects assistant tool_use blocks to be replayed exactly
		// before the next user tool_result turn.
		messages = append(messages, payloadMessage{
			Role:    "assistant",
			Content: response.assistantContent(),
		})

		results, err := executeToolCalls(ctx, toolCalls, executor)
		if err != nil {
			return "", err
		}
		messages = append(messages, payloadMessage{
			Role:    "user",
			Content: results,
		})
	}

	return "", fmt.Errorf("anthropic tool loop exceeded %d iterations", maxIterations)
}

type anthropicMessageRequest struct {
	System   string
	Messages []payloadMessage
	Tools    []anthropicTool
}

func (p *Provider) createMessage(
	ctx context.Context,
	request anthropicMessageRequest,
) (anthropicMessageResponse, error) {
	payload := struct {
		Model     string           `json:"model"`
		System    string           `json:"system,omitempty"`
		Messages  []payloadMessage `json:"messages"`
		MaxTokens int              `json:"max_tokens"`
		Stream    bool             `json:"stream"`
		Tools     []anthropicTool  `json:"tools,omitempty"`
	}{
		Model:     p.modelID,
		System:    request.System,
		Messages:  request.Messages,
		MaxTokens: p.maxTokens,
		Stream:    false,
		Tools:     request.Tools,
	}

	responseBody, err := p.postJSON(ctx, "/v1/messages", payload)
	if err != nil {
		return anthropicMessageResponse{}, err
	}

	var response anthropicMessageResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return anthropicMessageResponse{}, err
	}
	return response, nil
}

func (r anthropicMessageResponse) text() string {
	parts := make([]string, 0, len(r.Content))
	for _, block := range r.Content {
		if block.Type != "text" || strings.TrimSpace(block.Text) == "" {
			continue
		}
		parts = append(parts, strings.TrimSpace(block.Text))
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func (r anthropicMessageResponse) toolCalls() []model.ToolCall {
	calls := make([]model.ToolCall, 0, len(r.Content))
	for _, block := range r.Content {
		if block.Type != "tool_use" || strings.TrimSpace(block.Name) == "" {
			continue
		}
		calls = append(calls, model.ToolCall{
			ID:    strings.TrimSpace(block.ID),
			Name:  strings.TrimSpace(block.Name),
			Input: append(json.RawMessage(nil), block.Input...),
		})
	}
	return calls
}

func (r anthropicMessageResponse) assistantContent() []anthropicContentBlock {
	out := make([]anthropicContentBlock, 0, len(r.Content))
	for _, block := range r.Content {
		switch block.Type {
		case "text":
			if strings.TrimSpace(block.Text) == "" {
				continue
			}
			out = append(out, anthropicContentBlock{
				Type: "text",
				Text: block.Text,
			})
		case "tool_use":
			out = append(out, anthropicContentBlock{
				Type:  "tool_use",
				ID:    block.ID,
				Name:  block.Name,
				Input: append(json.RawMessage(nil), block.Input...),
			})
		}
	}
	return out
}

func normalizeMessages(messages []model.Message) []payloadMessage {
	out := make([]payloadMessage, 0, len(messages))
	for _, message := range messages {
		content := strings.TrimSpace(message.Content)
		if content == "" {
			continue
		}

		role := normalizeRole(message.Role)
		if role == "assistant" && len(out) == 0 {
			// Anthropic expects turns to start with a user message. When our
			// prompt window begins mid-conversation, drop leading assistant-only
			// context instead of injecting synthetic transcript text.
			continue
		}

		if len(out) > 0 && out[len(out)-1].Role == role {
			existing, ok := out[len(out)-1].Content.(string)
			if ok {
				out[len(out)-1].Content = existing + "\n\n" + content
				continue
			}
		}

		out = append(out, payloadMessage{
			Role:    role,
			Content: content,
		})
	}
	return out
}

func executeToolCalls(
	ctx context.Context,
	calls []model.ToolCall,
	executor model.ToolExecutor,
) ([]anthropicContentBlock, error) {
	results := make([]anthropicContentBlock, 0, len(calls))
	for _, call := range calls {
		result, err := executor(ctx, call)
		if err != nil {
			return nil, err
		}
		results = append(results, anthropicContentBlock{
			Type:      "tool_result",
			ToolUseID: result.ToolCallID,
			Content:   result.Content,
			IsError:   result.IsError,
		})
	}
	return results, nil
}

func toAnthropicTools(tools []model.Tool) []anthropicTool {
	if len(tools) == 0 {
		return nil
	}

	out := make([]anthropicTool, 0, len(tools))
	for _, tool := range tools {
		if strings.TrimSpace(tool.Name) == "" || len(tool.InputSchema) == 0 {
			continue
		}
		out = append(out, anthropicTool{
			Name:        strings.TrimSpace(tool.Name),
			Description: strings.TrimSpace(tool.Description),
			InputSchema: append(json.RawMessage(nil), tool.InputSchema...),
		})
	}
	return out
}

func normalizeRole(role string) string {
	if strings.EqualFold(strings.TrimSpace(role), "assistant") {
		return "assistant"
	}
	return "user"
}

func (p *Provider) postJSON(ctx context.Context, endpoint string, payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		p.baseURL+endpoint,
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("anthropic-version", p.version)
	if p.betaHeader != "" {
		req.Header.Set("anthropic-beta", p.betaHeader)
	}

	res, err := p.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	responseBody, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("model request failed: status=%d body=%s", res.StatusCode, string(responseBody))
	}
	return responseBody, nil
}

func normalizeBaseURL(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return defaultBaseURL
	}
	trimmed = strings.TrimRight(trimmed, "/")
	return strings.TrimRight(strings.TrimSuffix(trimmed, "/v1"), "/")
}

func normalizeHeaderList(value string) string {
	items := strings.Split(value, ",")
	out := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return strings.Join(out, ",")
}
