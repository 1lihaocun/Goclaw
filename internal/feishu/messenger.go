package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
)

const (
	// Feishu text/post messages have a finite edit budget, so message-update
	// streaming needs a much slower cadence than CardKit element updates.
	streamingMessageUpdatePeriod   = 2 * time.Second
	streamingMessageUpdateMaxEdits = 18
	feishuMessageEditLimitCode     = 230072
)

type MessengerConfig = ClientConfig

type Messenger struct {
	client *Client
}

type StreamingMessageRequest struct {
	ChatID           string
	ReplyToMessageID string
	InitialText      string
	MsgType          string
	ReplyInThread    bool
}

type StreamingMessageSession struct {
	client *Client

	mu               sync.Mutex
	sendResult       SendResult
	chatID           string
	replyToMessageID string
	replyInThread    bool
	messageID        string
	msgType          string
	currentText      string
	pendingText      string
	lastUpdate       time.Time
	editCount        int
	editLimitReached bool
	closed           bool
}

func NewMessenger(cfg MessengerConfig) *Messenger {
	return &Messenger{client: NewClient(cfg)}
}

func (m *Messenger) Configured() bool {
	return m != nil && m.client != nil && m.client.Configured()
}

func (m *Messenger) SendText(ctx context.Context, chatID, text string) (SendResult, error) {
	if m == nil || m.client == nil {
		return SendResult{}, ErrNotConfigured
	}
	return m.client.SendText(ctx, chatID, text)
}

func (m *Messenger) SendPost(ctx context.Context, chatID, contentJSON string) (SendResult, error) {
	if m == nil || m.client == nil {
		return SendResult{}, ErrNotConfigured
	}
	return m.client.SendPost(ctx, chatID, contentJSON)
}

func (m *Messenger) SendMessage(ctx context.Context, req SendMessageRequest) (SendResult, error) {
	if m == nil || m.client == nil {
		return SendResult{}, ErrNotConfigured
	}
	return m.client.SendMessage(ctx, req)
}

func (m *Messenger) ReplyMessage(ctx context.Context, req ReplyMessageRequest) (SendResult, error) {
	if m == nil || m.client == nil {
		return SendResult{}, ErrNotConfigured
	}
	return m.client.ReplyMessage(ctx, req)
}

func (m *Messenger) BeginStreamingMessageUpdate(
	ctx context.Context,
	req StreamingMessageRequest,
) (*StreamingMessageSession, error) {
	if m == nil || m.client == nil {
		return nil, ErrNotConfigured
	}

	msgType := normalizeStreamingMessageType(req.MsgType)
	initialText := strings.TrimSpace(req.InitialText)
	if initialText == "" {
		initialText = "⏳ Thinking..."
	}
	contentJSON := streamingMessageContentJSON(msgType, initialText)

	var (
		sendResult SendResult
		err        error
	)
	if strings.TrimSpace(req.ReplyToMessageID) != "" {
		sendResult, err = m.client.ReplyMessage(ctx, ReplyMessageRequest{
			MessageID:     strings.TrimSpace(req.ReplyToMessageID),
			MsgType:       msgType,
			ContentJSON:   contentJSON,
			ReplyInThread: req.ReplyInThread,
		})
	} else {
		sendResult, err = m.client.SendMessage(ctx, SendMessageRequest{
			ReceiveIDType: "chat_id",
			ReceiveID:     strings.TrimSpace(req.ChatID),
			MsgType:       msgType,
			ContentJSON:   contentJSON,
		})
	}
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(sendResult.ChatID) == "" {
		sendResult.ChatID = strings.TrimSpace(req.ChatID)
	}
	return &StreamingMessageSession{
		client:           m.client,
		sendResult:       sendResult,
		chatID:           strings.TrimSpace(sendResult.ChatID),
		replyToMessageID: strings.TrimSpace(req.ReplyToMessageID),
		replyInThread:    req.ReplyInThread,
		messageID:        strings.TrimSpace(sendResult.MessageID),
		msgType:          msgType,
		currentText:      initialText,
		lastUpdate:       time.Now(),
	}, nil
}

func (m *Messenger) AddMessageReaction(
	ctx context.Context,
	messageID string,
	emojiType string,
) (MessageReactionInfo, error) {
	if m == nil || m.client == nil {
		return MessageReactionInfo{}, ErrNotConfigured
	}
	return m.client.AddMessageReaction(ctx, messageID, emojiType)
}

func (m *Messenger) RemoveMessageReaction(
	ctx context.Context,
	messageID string,
	reactionID string,
) (MessageReactionInfo, error) {
	if m == nil || m.client == nil {
		return MessageReactionInfo{}, ErrNotConfigured
	}
	return m.client.RemoveMessageReaction(ctx, messageID, reactionID)
}

func (s *StreamingMessageSession) SendResult() SendResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sendResult
}

func (s *StreamingMessageSession) Update(ctx context.Context, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}
	if text == s.currentText || text == s.pendingText {
		return nil
	}
	if s.editLimitReached || s.editCount >= streamingMessageUpdateMaxEdits {
		s.editLimitReached = true
		s.pendingText = text
		return nil
	}
	if time.Since(s.lastUpdate) < streamingMessageUpdatePeriod {
		s.pendingText = text
		return nil
	}
	if err := s.updateLocked(ctx, text); err != nil {
		if isMessageEditLimitError(err) {
			s.editLimitReached = true
			s.pendingText = text
			return nil
		}
		return err
	}
	return nil
}

func (s *StreamingMessageSession) Close(ctx context.Context, finalText string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}
	s.closed = true
	finalText = firstNonEmptyString(finalText, s.pendingText, s.currentText)
	if strings.TrimSpace(finalText) == "" || finalText == s.currentText {
		s.pendingText = ""
		return nil
	}

	if !s.editLimitReached && s.editCount < streamingMessageUpdateMaxEdits {
		if err := s.updateLocked(ctx, finalText); err == nil {
			return nil
		} else if !isMessageEditLimitError(err) {
			return err
		}
		s.editLimitReached = true
	}

	return s.sendFinalMessageLocked(ctx, finalText)
}

func normalizeStreamingMessageType(msgType string) string {
	switch strings.ToLower(strings.TrimSpace(msgType)) {
	case "post":
		return "post"
	default:
		return "text"
	}
}

func streamingMessageContentJSON(msgType, text string) string {
	if normalizeStreamingMessageType(msgType) == "post" {
		return MarkdownPostContentJSON(text)
	}
	return TextContentJSON(text)
}

func (s *StreamingMessageSession) updateLocked(ctx context.Context, text string) error {
	if err := s.client.UpdateMessage(ctx, UpdateMessageRequest{
		MessageID:   s.messageID,
		MsgType:     s.msgType,
		ContentJSON: streamingMessageContentJSON(s.msgType, text),
	}); err != nil {
		return err
	}
	s.currentText = text
	s.pendingText = ""
	s.lastUpdate = time.Now()
	s.editCount++
	return nil
}

func (s *StreamingMessageSession) sendFinalMessageLocked(ctx context.Context, text string) error {
	contentJSON := streamingMessageContentJSON(s.msgType, text)

	var (
		sendResult SendResult
		err        error
	)
	if s.replyToMessageID != "" {
		sendResult, err = s.client.ReplyMessage(ctx, ReplyMessageRequest{
			MessageID:     s.replyToMessageID,
			MsgType:       s.msgType,
			ContentJSON:   contentJSON,
			ReplyInThread: s.replyInThread,
		})
	} else {
		sendResult, err = s.client.SendMessage(ctx, SendMessageRequest{
			ReceiveIDType: "chat_id",
			ReceiveID:     s.chatID,
			MsgType:       s.msgType,
			ContentJSON:   contentJSON,
		})
	}
	if err != nil {
		return err
	}
	if strings.TrimSpace(sendResult.ChatID) == "" {
		sendResult.ChatID = s.chatID
	}
	s.sendResult = sendResult
	s.chatID = strings.TrimSpace(sendResult.ChatID)
	s.messageID = strings.TrimSpace(sendResult.MessageID)
	s.currentText = text
	s.pendingText = ""
	return nil
}

func isMessageEditLimitError(err error) bool {
	if err == nil {
		return false
	}

	var (
		requestErr *RequestError
		apiErr     *APIError
		body       []byte
	)
	switch {
	case errors.As(err, &requestErr):
		body = requestErr.Body
	case errors.As(err, &apiErr):
		body = apiErr.Body
	default:
		return false
	}

	var payload struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return false
	}
	return payload.Code == feishuMessageEditLimitCode
}
