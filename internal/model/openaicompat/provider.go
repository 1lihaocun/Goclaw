package openaicompat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"goclaw/internal/model"
)

const defaultBaseURL = "https://api.openai.com/v1"
const maxRequestAttempts = 3

type Config struct {
	API               model.API
	BaseURL           string
	APIKey            string
	ModelID           string
	DisableAuthHeader bool
	HTTPClient        *http.Client
}

type Provider struct {
	api               model.API
	baseURL           string
	apiKey            string
	modelID           string
	disableAuthHeader bool
	httpClient        *http.Client
}

func New(cfg Config) *Provider {
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	api := cfg.API
	if api == "" {
		api = model.APIOpenAICompletions
	}
	return &Provider{
		api:               api,
		baseURL:           normalizeBaseURL(cfg.BaseURL),
		apiKey:            strings.TrimSpace(cfg.APIKey),
		modelID:           strings.TrimSpace(cfg.ModelID),
		disableAuthHeader: cfg.DisableAuthHeader,
		httpClient:        httpClient,
	}
}

func (p *Provider) Name() string {
	return "openai-compatible"
}

func (p *Provider) Generate(ctx context.Context, request model.Request) (string, error) {
	if err := p.validateConfigured(); err != nil {
		return "", err
	}

	switch p.api {
	case model.APIOpenAIResponses, model.APIOpenAICodexResponses:
		return p.generateResponses(ctx, request)
	case model.APIOpenAICompletions:
		return p.generateChatCompletions(ctx, request)
	default:
		return "", fmt.Errorf("unsupported model api %q", p.api)
	}
}

func (p *Provider) validateConfigured() error {
	if !p.disableAuthHeader && p.apiKey == "" {
		return fmt.Errorf("model api key is not configured")
	}
	if p.modelID == "" {
		return fmt.Errorf("model id is not configured")
	}
	return nil
}

func (p *Provider) generateChatCompletions(ctx context.Context, request model.Request) (string, error) {
	messages := toChatCompletionMessages(request)
	payload := struct {
		Model    string                   `json:"model"`
		Messages []chatCompletionsMessage `json:"messages"`
	}{
		Model:    p.modelID,
		Messages: messages,
	}

	responseBody, err := p.postJSON(ctx, "/chat/completions", payload)
	if err != nil {
		return "", err
	}

	var response struct {
		Choices []struct {
			Message struct {
				Content any `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return "", err
	}
	if len(response.Choices) == 0 {
		return "", nil
	}
	return extractOpenAIContent(response.Choices[0].Message.Content), nil
}

func (p *Provider) generateResponses(ctx context.Context, request model.Request) (string, error) {
	type inputMessage struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	payload := struct {
		Model        string         `json:"model"`
		Instructions string         `json:"instructions,omitempty"`
		Input        []inputMessage `json:"input"`
	}{
		Model:        p.modelID,
		Instructions: strings.TrimSpace(request.System),
		Input:        make([]inputMessage, 0, len(request.Messages)),
	}
	for _, message := range request.Messages {
		payload.Input = append(payload.Input, inputMessage{
			Role:    message.Role,
			Content: message.Content,
		})
	}

	responseBody, err := p.postJSON(ctx, "/responses", payload)
	if err != nil {
		return "", err
	}

	var response struct {
		OutputText string `json:"output_text"`
		Output     []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			Text string `json:"text"`
		} `json:"output"`
	}
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return "", err
	}
	if text := strings.TrimSpace(response.OutputText); text != "" {
		return text, nil
	}
	for _, output := range response.Output {
		if output.Type == "output_text" && strings.TrimSpace(output.Text) != "" {
			return strings.TrimSpace(output.Text), nil
		}
		for _, content := range output.Content {
			if content.Type == "output_text" && strings.TrimSpace(content.Text) != "" {
				return strings.TrimSpace(content.Text), nil
			}
		}
	}
	return "", nil
}

func (p *Provider) postJSON(ctx context.Context, endpoint string, payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	var lastErr error
	for attempt := 1; attempt <= maxRequestAttempts; attempt++ {
		responseBody, err := p.postJSONOnce(ctx, endpoint, body)
		if err == nil {
			return responseBody, nil
		}
		lastErr = err
		if !shouldRetryRequest(ctx, err) {
			return nil, err
		}
	}
	return nil, lastErr
}

func (p *Provider) postJSONOnce(ctx context.Context, endpoint string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		p.baseURL+endpoint,
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, err
	}
	if !p.disableAuthHeader {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	req.Header.Set("Content-Type", "application/json")

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
		return nil, &retryableStatusError{StatusCode: res.StatusCode, Body: string(responseBody)}
	}
	return responseBody, nil
}

type retryableStatusError struct {
	StatusCode int
	Body       string
}

func (e *retryableStatusError) Error() string {
	return fmt.Sprintf("model request failed: status=%d body=%s", e.StatusCode, e.Body)
}

func shouldRetryRequest(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	if ctx != nil && ctx.Err() != nil {
		return false
	}
	var statusErr *retryableStatusError
	if errors.As(err, &statusErr) {
		return statusErr.StatusCode == http.StatusRequestTimeout ||
			statusErr.StatusCode == http.StatusTooManyRequests ||
			statusErr.StatusCode >= http.StatusInternalServerError
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return errors.Is(err, context.DeadlineExceeded)
}

func normalizeBaseURL(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return defaultBaseURL
	}
	return strings.TrimRight(trimmed, "/")
}

func extractOpenAIContent(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			record, ok := item.(map[string]any)
			if !ok {
				continue
			}
			text, _ := record["text"].(string)
			if strings.TrimSpace(text) != "" {
				parts = append(parts, strings.TrimSpace(text))
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}
