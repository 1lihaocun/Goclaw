package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	streamingCardElementID    = "content"
	streamingCardUpdatePeriod = 100 * time.Millisecond
)

type CreateCardRequest struct {
	CardJSON string
}

type StreamingCardRequest struct {
	ChatID           string
	ReplyToMessageID string
	InitialText      string
	ReplyInThread    bool
}

type StreamingCardSession struct {
	client *Client

	mu          sync.Mutex
	sendResult  SendResult
	cardID      string
	sequence    int
	currentText string
	pendingText string
	lastUpdate  time.Time
	closed      bool
}

func (c *Client) CreateCard(ctx context.Context, req CreateCardRequest) (string, error) {
	cardJSON := strings.TrimSpace(req.CardJSON)
	if cardJSON == "" || !json.Valid([]byte(cardJSON)) {
		return "", fmt.Errorf("invalid card json")
	}

	body, err := c.callJSON(ctx, http.MethodPost, "/cardkit/v1/cards", map[string]any{
		"type": "card_json",
		"data": cardJSON,
	})
	if err != nil {
		return "", err
	}

	var response struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			CardID string `json:"card_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return "", err
	}
	if response.Code != 0 || strings.TrimSpace(response.Data.CardID) == "" {
		return "", newAPIError("feishu create card failed", response.Code, response.Msg, body)
	}
	return strings.TrimSpace(response.Data.CardID), nil
}

func (c *Client) UpdateCardElementContent(
	ctx context.Context,
	cardID string,
	elementID string,
	content string,
	sequence int,
	uuid string,
) error {
	body, err := c.callJSON(ctx, http.MethodPut, "/cardkit/v1/cards/"+url.PathEscape(strings.TrimSpace(cardID))+"/elements/"+url.PathEscape(strings.TrimSpace(elementID))+"/content", map[string]any{
		"content":  content,
		"sequence": sequence,
		"uuid":     strings.TrimSpace(uuid),
	})
	if err != nil {
		return err
	}
	return decodeCodeOnly(body, "feishu update card element failed")
}

func (c *Client) UpdateCardSettings(
	ctx context.Context,
	cardID string,
	settingsJSON string,
	sequence int,
	uuid string,
) error {
	settingsJSON = strings.TrimSpace(settingsJSON)
	if settingsJSON == "" || !json.Valid([]byte(settingsJSON)) {
		return fmt.Errorf("invalid card settings json")
	}
	body, err := c.callJSON(ctx, http.MethodPatch, "/cardkit/v1/cards/"+url.PathEscape(strings.TrimSpace(cardID))+"/settings", map[string]any{
		"settings": settingsJSON,
		"sequence": sequence,
		"uuid":     strings.TrimSpace(uuid),
	})
	if err != nil {
		return err
	}
	return decodeCodeOnly(body, "feishu update card settings failed")
}

func newStreamingCardSession(
	client *Client,
	sendResult SendResult,
	cardID string,
	initialText string,
) *StreamingCardSession {
	return &StreamingCardSession{
		client:      client,
		sendResult:  sendResult,
		cardID:      strings.TrimSpace(cardID),
		sequence:    1,
		currentText: strings.TrimSpace(initialText),
		lastUpdate:  time.Now(),
	}
}

func (m *Messenger) BeginStreamingCard(
	ctx context.Context,
	req StreamingCardRequest,
) (*StreamingCardSession, error) {
	if m == nil || m.client == nil {
		return nil, ErrNotConfigured
	}

	initialText := strings.TrimSpace(req.InitialText)
	if initialText == "" {
		initialText = "⏳ Thinking..."
	}

	cardID, err := m.client.CreateCard(ctx, CreateCardRequest{
		CardJSON: StreamingMarkdownCardJSON(initialText),
	})
	if err != nil {
		return nil, err
	}

	contentJSON := CardReferenceContentJSON(cardID)
	var sendResult SendResult
	if strings.TrimSpace(req.ReplyToMessageID) != "" {
		sendResult, err = m.client.ReplyMessage(ctx, ReplyMessageRequest{
			MessageID:     strings.TrimSpace(req.ReplyToMessageID),
			MsgType:       "interactive",
			ContentJSON:   contentJSON,
			ReplyInThread: req.ReplyInThread,
		})
	} else {
		sendResult, err = m.client.SendMessage(ctx, SendMessageRequest{
			ReceiveIDType: "chat_id",
			ReceiveID:     strings.TrimSpace(req.ChatID),
			MsgType:       "interactive",
			ContentJSON:   contentJSON,
		})
	}
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(sendResult.ChatID) == "" {
		sendResult.ChatID = strings.TrimSpace(req.ChatID)
	}
	return newStreamingCardSession(m.client, sendResult, cardID, initialText), nil
}

func (s *StreamingCardSession) SendResult() SendResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sendResult
}

func (s *StreamingCardSession) Update(ctx context.Context, text string) error {
	if strings.TrimSpace(text) == "" {
		return nil
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	if text == s.currentText {
		s.mu.Unlock()
		return nil
	}
	if time.Since(s.lastUpdate) < streamingCardUpdatePeriod {
		s.pendingText = text
		s.mu.Unlock()
		return nil
	}
	s.sequence++
	sequence := s.sequence
	cardID := s.cardID
	s.currentText = text
	s.pendingText = ""
	s.lastUpdate = time.Now()
	s.mu.Unlock()

	return s.client.UpdateCardElementContent(
		ctx,
		cardID,
		streamingCardElementID,
		text,
		sequence,
		fmt.Sprintf("s_%s_%d", cardID, sequence),
	)
}

func (s *StreamingCardSession) Close(ctx context.Context, finalText string) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	if strings.TrimSpace(finalText) == "" {
		finalText = firstNonEmptyString(s.pendingText, s.currentText)
	}
	cardID := s.cardID
	updateNeeded := finalText != "" && finalText != s.currentText
	var updateSequence int
	if updateNeeded {
		s.sequence++
		updateSequence = s.sequence
		s.currentText = finalText
	}
	s.pendingText = ""
	s.sequence++
	settingsSequence := s.sequence
	summaryText := truncateStreamingSummary(firstNonEmptyString(finalText, s.currentText))
	s.mu.Unlock()

	if updateNeeded {
		if err := s.client.UpdateCardElementContent(
			ctx,
			cardID,
			streamingCardElementID,
			finalText,
			updateSequence,
			fmt.Sprintf("s_%s_%d", cardID, updateSequence),
		); err != nil {
			return err
		}
	}

	settingsJSON := string(mustJSON(map[string]any{
		"config": map[string]any{
			"streaming_mode": false,
			"summary": map[string]any{
				"content": summaryText,
			},
		},
	}))
	return s.client.UpdateCardSettings(
		ctx,
		cardID,
		settingsJSON,
		settingsSequence,
		fmt.Sprintf("c_%s_%d", cardID, settingsSequence),
	)
}

func truncateStreamingSummary(text string) string {
	const maxSummaryRunes = 50
	text = strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
	if text == "" {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= maxSummaryRunes {
		return text
	}
	return string(runes[:maxSummaryRunes-3]) + "..."
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}
