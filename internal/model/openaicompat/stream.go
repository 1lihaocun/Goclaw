package openaicompat

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

var errSSEStreamDone = errors.New("sse stream done")

func (p *Provider) SupportsStreaming() bool {
	switch p.api {
	case model.APIOpenAICompletions, model.APIOpenAIResponses, model.APIOpenAICodexResponses:
		return true
	default:
		return false
	}
}

func (p *Provider) Stream(
	ctx context.Context,
	request model.Request,
	handler model.StreamHandler,
) (string, error) {
	if !p.SupportsStreaming() {
		return "", fmt.Errorf("streaming is unsupported for model api %q", p.api)
	}
	if err := p.validateConfigured(); err != nil {
		return "", err
	}

	switch p.api {
	case model.APIOpenAIResponses, model.APIOpenAICodexResponses:
		return p.streamResponses(ctx, request, handler)
	case model.APIOpenAICompletions:
		return p.streamChatCompletions(ctx, request, handler)
	default:
		return "", fmt.Errorf("streaming is unsupported for model api %q", p.api)
	}
}

func (p *Provider) streamResponses(
	ctx context.Context,
	request model.Request,
	handler model.StreamHandler,
) (string, error) {
	payload := struct {
		Model        string `json:"model"`
		Instructions string `json:"instructions,omitempty"`
		Input        []any  `json:"input"`
		Stream       bool   `json:"stream"`
	}{
		Model:        p.modelID,
		Instructions: strings.TrimSpace(request.System),
		Input:        toResponsesInput(request.Messages),
		Stream:       true,
	}

	response, err := p.postJSONStream(ctx, "/responses", payload)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()

	finalText := ""
	err = model.ConsumeSSE(response.Body, func(event model.SSEEvent) error {
		if strings.TrimSpace(event.Data) == "" {
			return nil
		}
		if strings.TrimSpace(event.Data) == "[DONE]" {
			return errSSEStreamDone
		}

		var envelope struct {
			Type  string `json:"type"`
			Delta string `json:"delta,omitempty"`
			Text  string `json:"text,omitempty"`
			Item  struct {
				Type    string `json:"type"`
				Role    string `json:"role,omitempty"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text,omitempty"`
				} `json:"content,omitempty"`
			} `json:"item,omitempty"`
			Error struct {
				Message string `json:"message"`
			} `json:"error,omitempty"`
			Message string `json:"message,omitempty"`
		}
		if err := json.Unmarshal([]byte(event.Data), &envelope); err != nil {
			return err
		}

		switch envelope.Type {
		case "response.output_text.delta":
			return appendStreamDelta(handler, &finalText, envelope.Delta)
		case "response.output_text.done":
			return appendStreamSnapshot(handler, &finalText, envelope.Text)
		case "response.output_item.done":
			if envelope.Item.Type != "message" {
				return nil
			}
			var snapshot strings.Builder
			for _, content := range envelope.Item.Content {
				if content.Type != "output_text" {
					continue
				}
				snapshot.WriteString(content.Text)
			}
			return appendStreamSnapshot(handler, &finalText, snapshot.String())
		case "error":
			return streamEventError(firstNonEmpty(strings.TrimSpace(envelope.Error.Message), strings.TrimSpace(envelope.Message)))
		default:
			return nil
		}
	})
	if errors.Is(err, errSSEStreamDone) {
		err = nil
	}
	return finalText, err
}

func (p *Provider) streamChatCompletions(
	ctx context.Context,
	request model.Request,
	handler model.StreamHandler,
) (string, error) {
	payload := struct {
		Model    string                   `json:"model"`
		Messages []chatCompletionsMessage `json:"messages"`
		Stream   bool                     `json:"stream"`
	}{
		Model:    p.modelID,
		Messages: toChatCompletionMessages(request),
		Stream:   true,
	}

	response, err := p.postJSONStream(ctx, "/chat/completions", payload)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()

	finalText := ""
	err = model.ConsumeSSE(response.Body, func(event model.SSEEvent) error {
		if strings.TrimSpace(event.Data) == "" {
			return nil
		}
		if strings.TrimSpace(event.Data) == "[DONE]" {
			return errSSEStreamDone
		}

		var envelope struct {
			Choices []struct {
				Delta struct {
					Content any `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
			Error struct {
				Message string `json:"message"`
			} `json:"error,omitempty"`
		}
		if err := json.Unmarshal([]byte(event.Data), &envelope); err != nil {
			return err
		}
		if len(envelope.Choices) == 0 {
			if strings.TrimSpace(envelope.Error.Message) == "" {
				return nil
			}
			return streamEventError(strings.TrimSpace(envelope.Error.Message))
		}
		return appendStreamDelta(handler, &finalText, extractOpenAIStreamDelta(envelope.Choices[0].Delta.Content))
	})
	if errors.Is(err, errSSEStreamDone) {
		err = nil
	}
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

	var lastErr error
	for attempt := 1; attempt <= maxRequestAttempts; attempt++ {
		response, err := p.postJSONStreamOnce(ctx, endpoint, body)
		if err == nil {
			return response, nil
		}
		lastErr = err
		if !shouldRetryRequest(ctx, err) {
			return nil, err
		}
	}
	return nil, lastErr
}

func (p *Provider) postJSONStreamOnce(
	ctx context.Context,
	endpoint string,
	body []byte,
) (*http.Response, error) {
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
	req.Header.Set("Accept", "text/event-stream")

	res, err := p.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		defer res.Body.Close()
		responseBody, readErr := ioReadAll(res)
		if readErr != nil {
			return nil, readErr
		}
		return nil, &retryableStatusError{StatusCode: res.StatusCode, Body: string(responseBody)}
	}
	return res, nil
}

func appendStreamDelta(
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

func appendStreamSnapshot(
	handler model.StreamHandler,
	fullText *string,
	snapshot string,
) error {
	if snapshot == "" {
		return nil
	}
	if *fullText == snapshot {
		return nil
	}
	if strings.HasPrefix(snapshot, *fullText) {
		return appendStreamDelta(handler, fullText, snapshot[len(*fullText):])
	}
	if *fullText != "" {
		return nil
	}
	return appendStreamDelta(handler, fullText, snapshot)
}

func streamEventError(message string) error {
	if strings.TrimSpace(message) == "" {
		message = "streaming request failed"
	}
	return errors.New(message)
}

func ioReadAll(response *http.Response) ([]byte, error) {
	if response == nil || response.Body == nil {
		return nil, nil
	}
	return io.ReadAll(response.Body)
}

func extractOpenAIStreamDelta(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []any:
		var builder strings.Builder
		for _, item := range typed {
			record, ok := item.(map[string]any)
			if !ok {
				continue
			}
			text, _ := record["text"].(string)
			builder.WriteString(text)
		}
		return builder.String()
	default:
		return ""
	}
}
