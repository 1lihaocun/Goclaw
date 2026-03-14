package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"goclaw/internal/model"
)

func (p *Provider) SupportsStreaming() bool {
	return true
}

func (p *Provider) Stream(
	ctx context.Context,
	request model.Request,
	handler model.StreamHandler,
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

	payload := struct {
		Model     string           `json:"model"`
		System    string           `json:"system,omitempty"`
		Messages  []payloadMessage `json:"messages"`
		MaxTokens int              `json:"max_tokens"`
		Stream    bool             `json:"stream"`
	}{
		Model:     p.modelID,
		System:    strings.TrimSpace(request.System),
		Messages:  messages,
		MaxTokens: p.maxTokens,
		Stream:    true,
	}

	response, err := p.postJSONStream(ctx, "/v1/messages", payload)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()

	finalText := ""
	err = model.ConsumeSSE(response.Body, func(event model.SSEEvent) error {
		if strings.TrimSpace(event.Data) == "" {
			return nil
		}

		var envelope struct {
			Type  string `json:"type"`
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text,omitempty"`
			} `json:"delta,omitempty"`
			Error struct {
				Message string `json:"message"`
			} `json:"error,omitempty"`
			Message string `json:"message,omitempty"`
		}
		if err := json.Unmarshal([]byte(event.Data), &envelope); err != nil {
			return err
		}

		switch envelope.Type {
		case "content_block_delta":
			if envelope.Delta.Type != "text_delta" {
				return nil
			}
			return appendAnthropicDelta(handler, &finalText, envelope.Delta.Text)
		case "error":
			return anthropicStreamError(firstNonEmpty(strings.TrimSpace(envelope.Error.Message), strings.TrimSpace(envelope.Message)))
		default:
			return nil
		}
	})
	return finalText, err
}

func (p *Provider) postJSONStream(
	ctx context.Context,
	endpoint string,
	payload any,
) (*http.Response, error) {
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
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("anthropic-version", p.version)
	if p.betaHeader != "" {
		req.Header.Set("anthropic-beta", p.betaHeader)
	}

	res, err := p.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		defer res.Body.Close()
		body, readErr := io.ReadAll(res.Body)
		if readErr != nil {
			return nil, readErr
		}
		return nil, fmt.Errorf("model request failed: status=%d body=%s", res.StatusCode, string(body))
	}
	return res, nil
}

func appendAnthropicDelta(
	handler model.StreamHandler,
	fullText *string,
	delta string,
) error {
	if delta == "" {
		return nil
	}
	*fullText += delta
	if handler == nil {
		return nil
	}
	return handler(model.StreamEvent{
		Type: model.StreamEventAssistantDelta,
		Text: delta,
	})
}

func anthropicStreamError(message string) error {
	if strings.TrimSpace(message) == "" {
		message = "anthropic streaming request failed"
	}
	return errors.New(message)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
