package feishu

import "context"

type MessengerConfig = ClientConfig

type Messenger struct {
	client *Client
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
