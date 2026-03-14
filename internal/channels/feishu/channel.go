package feishuchannel

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	channelcore "goclaw/internal/channels"
	"goclaw/internal/config"
	"goclaw/internal/domain"
	rawfeishu "goclaw/internal/feishu"
	rawlongpoll "goclaw/internal/feishu/longpoll"
)

const DefaultAccountID = "default"

var (
	feishuFencedCodeBlockPattern = regexp.MustCompile("```[\\s\\S]*?```")
	feishuMarkdownTablePattern   = regexp.MustCompile(`\|.+\|[\r\n]+\|[-:| ]+\|`)
)

type accountSettings struct {
	Name                   string
	RenderMode             string
	StreamingToolSummaries string
	Actions                config.FeishuActionsConfig
	ProcessingAckEmoji     string
	ProfileID              domain.ProfileID
	ConnectionMode         string
	WebhookHost            string
	WebhookPort            int
	WebhookPath            string
	APIBaseURL             string
	AppID                  string
	AppSecret              string
	EncryptKey             string
	VerificationToken      string
	MaxBodyBytes           int64
}

type resolvedAccount struct {
	AccountID   string
	Enabled     bool
	Configured  bool
	Settings    accountSettings
	TransportID string
}

type longpollRunner interface {
	Run(ctx context.Context) error
}

type Channel struct {
	config    config.FeishuConfig
	accounts  []resolvedAccount
	outbounds []channelcore.Outbound
	logger    *slog.Logger

	runtimeMu         sync.Mutex
	runtimeBinding    *runtimeBinding
	longpollRuntimes  map[string]*managedLongpollRuntime
	newLongpollRunner func(params rawlongpoll.Params) (longpollRunner, error)
}

func New(cfg config.FeishuConfig, logger *slog.Logger) *Channel {
	if logger == nil {
		logger = slog.Default()
	}

	accounts := resolveAccounts(cfg)
	outbounds := make([]channelcore.Outbound, 0, len(accounts))
	for _, account := range accounts {
		if !account.Enabled {
			continue
		}
		outbounds = append(outbounds, newOutbound(account))
	}

	return &Channel{
		config:           cfg,
		accounts:         accounts,
		outbounds:        outbounds,
		logger:           logger,
		longpollRuntimes: make(map[string]*managedLongpollRuntime),
		newLongpollRunner: func(params rawlongpoll.Params) (longpollRunner, error) {
			return rawlongpoll.New(params)
		},
	}
}

func (c *Channel) Provider() domain.Provider {
	return domain.ProviderFeishu
}

func (c *Channel) Outbounds() []channelcore.Outbound {
	outbounds := make([]channelcore.Outbound, 0, len(c.outbounds))
	outbounds = append(outbounds, c.outbounds...)
	return outbounds
}

func (c *Channel) HTTPRoutes(params channelcore.BuildRuntimeParams) ([]channelcore.HTTPRoute, error) {
	routes := make([]channelcore.HTTPRoute, 0)
	for _, account := range c.accounts {
		if !account.Enabled || !isWebhookMode(account.Settings.ConnectionMode) {
			continue
		}

		handler := rawfeishu.NewWebhookHandler(rawfeishu.WebhookHandlerParams{
			ProfileID:         account.Settings.ProfileID,
			EncryptKey:        account.Settings.EncryptKey,
			VerificationToken: account.Settings.VerificationToken,
			MaxBodyBytes:      account.Settings.MaxBodyBytes,
			Ingestor:          newMessageEventIngestor(account.Settings.ProfileID, params.Ingestor),
			EventObserver:     newEventObserver(account.Settings.ProfileID, params.EventObserver),
		})
		routes = append(routes, channelcore.HTTPRoute{
			Name:    account.TransportID,
			Address: net.JoinHostPort(account.Settings.WebhookHost, strconv.Itoa(account.Settings.WebhookPort)),
			Path:    account.Settings.WebhookPath,
			Handler: handler,
		})
	}
	return routes, nil
}

func (c *Channel) Transports(params channelcore.BuildRuntimeParams) ([]channelcore.Transport, error) {
	transports := make([]channelcore.Transport, 0)
	for _, account := range c.accounts {
		if !account.Enabled || !isLongpollMode(account.Settings.ConnectionMode) {
			continue
		}
		if c.longpollLifecycleAvailable() {
			continue
		}

		runner, err := c.buildLongpollRunner(account, params)
		if err != nil {
			return nil, err
		}

		transports = append(transports, runnerTransport{
			name:   account.TransportID,
			logger: params.Logger,
			run:    runner.Run,
		})
	}
	return transports, nil
}

func (c *Channel) buildLongpollRunner(
	account resolvedAccount,
	params channelcore.BuildRuntimeParams,
) (longpollRunner, error) {
	if c == nil || c.newLongpollRunner == nil {
		return nil, fmt.Errorf("feishu channel: longpoll runner factory is unavailable")
	}
	return c.newLongpollRunner(rawlongpoll.Params{
		ProfileID:     account.Settings.ProfileID,
		AppID:         account.Settings.AppID,
		AppSecret:     account.Settings.AppSecret,
		BaseURL:       account.Settings.APIBaseURL,
		Ingestor:      newMessageEventIngestor(account.Settings.ProfileID, params.Ingestor),
		EventObserver: newEventObserver(account.Settings.ProfileID, params.EventObserver),
		Logger:        params.Logger,
	})
}

type outbound struct {
	account   resolvedAccount
	messenger *rawfeishu.Messenger
}

func newOutbound(account resolvedAccount) *outbound {
	return &outbound{
		account: account,
		messenger: rawfeishu.NewMessenger(rawfeishu.MessengerConfig{
			BaseURL:   account.Settings.APIBaseURL,
			AppID:     account.Settings.AppID,
			AppSecret: account.Settings.AppSecret,
		}),
	}
}

func (o *outbound) ProfileID() domain.ProfileID {
	return o.account.Settings.ProfileID
}

func (o *outbound) Provider() domain.Provider {
	return domain.ProviderFeishu
}

func (o *outbound) Configured() bool {
	return o.account.Enabled && o.messenger.Configured()
}

func (o *outbound) SendText(ctx context.Context, roomID, text string) (channelcore.SendResult, error) {
	result, err := o.sendMarkdownText(ctx, roomID, "", text)
	if err != nil {
		return channelcore.SendResult{}, err
	}
	return toChannelSendResult(result), nil
}

func (o *outbound) SendReplyText(
	ctx context.Context,
	roomID, replyToMessageID, text string,
) (channelcore.SendResult, error) {
	result, err := o.sendMarkdownText(ctx, roomID, replyToMessageID, text)
	if err != nil {
		return channelcore.SendResult{}, err
	}
	return toChannelSendResult(result), nil
}

func (o *outbound) BeginStreamingReply(
	ctx context.Context,
	target channelcore.StreamingReplyTarget,
) (channelcore.StreamingReplySession, error) {
	if !o.streamingEnabled() {
		return nil, fmt.Errorf("feishu channel: streaming replies are unavailable")
	}
	return &feishuStreamingReplySession{
		outbound: o,
		target: channelcore.StreamingReplyTarget{
			RoomID:           strings.TrimSpace(target.RoomID),
			ReplyToMessageID: strings.TrimSpace(target.ReplyToMessageID),
		},
	}, nil
}

func (o *outbound) sendMarkdownText(
	ctx context.Context,
	roomID, replyToMessageID, text string,
) (rawfeishu.SendResult, error) {
	roomID = strings.TrimSpace(roomID)
	replyToMessageID = strings.TrimSpace(replyToMessageID)

	if replyToMessageID != "" {
		if o.preferCard(text) && o.account.Settings.Actions.MessageSend {
			result, err := o.messenger.ReplyMessage(ctx, rawfeishu.ReplyMessageRequest{
				MessageID:   replyToMessageID,
				MsgType:     "interactive",
				ContentJSON: rawfeishu.MarkdownCardContentJSON(text),
			})
			if err == nil {
				return result, nil
			}
		}
		if o.account.Settings.Actions.PostMessages {
			result, err := o.messenger.ReplyMessage(ctx, rawfeishu.ReplyMessageRequest{
				MessageID:   replyToMessageID,
				MsgType:     "post",
				ContentJSON: rawfeishu.MarkdownPostContentJSON(text),
			})
			if err == nil {
				return result, nil
			}
		}
		if o.account.Settings.Actions.MessageSend {
			result, err := o.messenger.ReplyMessage(ctx, rawfeishu.ReplyMessageRequest{
				MessageID:   replyToMessageID,
				MsgType:     "text",
				ContentJSON: rawfeishu.TextContentJSON(text),
			})
			if err == nil {
				return result, nil
			}
		}
	}

	if o.preferCard(text) && o.account.Settings.Actions.MessageSend {
		result, err := o.messenger.SendMessage(ctx, rawfeishu.SendMessageRequest{
			ReceiveIDType: "chat_id",
			ReceiveID:     roomID,
			MsgType:       "interactive",
			ContentJSON:   rawfeishu.MarkdownCardContentJSON(text),
		})
		if err == nil {
			return result, nil
		}
	}
	if o.account.Settings.Actions.PostMessages {
		result, err := o.messenger.SendPost(ctx, roomID, rawfeishu.MarkdownPostContentJSON(text))
		if err == nil {
			return result, nil
		}
	}
	return o.messenger.SendText(ctx, roomID, text)
}

func (o *outbound) preferCard(text string) bool {
	switch normalizeRenderMode(o.account.Settings.RenderMode) {
	case "card":
		return true
	case "raw":
		return false
	default:
		return shouldUseFeishuMarkdownCard(text)
	}
}

func (o *outbound) streamingEnabled() bool {
	return o.account.Settings.Actions.MessageSend || o.account.Settings.Actions.PostMessages
}

func shouldUseFeishuMarkdownCard(text string) bool {
	return feishuFencedCodeBlockPattern.MatchString(text) || feishuMarkdownTablePattern.MatchString(text)
}

func toChannelSendResult(result rawfeishu.SendResult) channelcore.SendResult {
	return channelcore.SendResult{
		MessageID: result.MessageID,
		RoomID:    result.ChatID,
	}
}

type feishuStreamingReplySession struct {
	outbound *outbound
	target   channelcore.StreamingReplyTarget

	mu             sync.Mutex
	session        feishuStreamingSession
	bufferedText   string
	bufferedUpdate int
	toolMessages   map[string]*toolCompanionMessage
}

type toolCompanionMessage struct {
	session feishuStreamingSession
	text    string
}

func (s *feishuStreamingReplySession) SendResult() channelcore.SendResult {
	session := s.currentSession()
	if session == nil {
		return channelcore.SendResult{}
	}
	return toChannelSendResult(session.SendResult())
}

func (s *feishuStreamingReplySession) UpdateText(ctx context.Context, text string) error {
	session, deliverText, err := s.ensureSession(ctx, text, false)
	if err != nil || session == nil || deliverText == "" {
		return err
	}
	return session.Update(ctx, deliverText)
}

func (s *feishuStreamingReplySession) Close(ctx context.Context, finalText string) error {
	session, deliverText, err := s.ensureSession(ctx, finalText, true)
	if err != nil {
		return err
	}
	if session != nil {
		if err := session.Close(ctx, deliverText); err != nil {
			return err
		}
	}
	return s.closeToolMessages(ctx)
}

type feishuStreamingSession interface {
	SendResult() rawfeishu.SendResult
	Update(context.Context, string) error
	Close(context.Context, string) error
}

func (s *feishuStreamingReplySession) currentSession() feishuStreamingSession {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.session
}

func (s *feishuStreamingReplySession) ensureSession(
	ctx context.Context,
	text string,
	closing bool,
) (feishuStreamingSession, string, error) {
	if s == nil || s.outbound == nil || s.outbound.messenger == nil {
		return nil, "", nil
	}
	text = strings.TrimSpace(text)
	s.mu.Lock()
	if text != "" {
		s.bufferedText = text
	}
	if s.session != nil {
		session := s.session
		deliverText := s.bufferedText
		s.mu.Unlock()
		return session, deliverText, nil
	}
	if text != "" && !closing {
		s.bufferedUpdate++
	}
	deliverText := s.bufferedText
	if !s.shouldStartSessionLocked(deliverText, closing) {
		s.mu.Unlock()
		return nil, "", nil
	}
	s.mu.Unlock()

	session, err := s.startSession(ctx, deliverText)
	if err != nil {
		return nil, "", err
	}

	s.mu.Lock()
	if s.session == nil {
		s.session = session
	}
	current := s.session
	deliverText = s.bufferedText
	s.mu.Unlock()
	return current, deliverText, nil
}

func (s *feishuStreamingReplySession) shouldStartSessionLocked(
	text string,
	closing bool,
) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}
	if closing {
		return true
	}
	if s == nil || s.outbound == nil {
		return true
	}
	if normalizeRenderMode(s.outbound.account.Settings.RenderMode) != "auto" {
		return true
	}
	if s.shouldUseStreamingCard(text) {
		return true
	}
	return !shouldDelayFeishuAutoStreamingStart(text, s.bufferedUpdate)
}

func (s *feishuStreamingReplySession) startSession(
	ctx context.Context,
	text string,
) (feishuStreamingSession, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, nil
	}
	if s.shouldUseStreamingCard(text) {
		session, err := s.outbound.messenger.BeginStreamingCard(ctx, rawfeishu.StreamingCardRequest{
			ChatID:           s.target.RoomID,
			ReplyToMessageID: s.target.ReplyToMessageID,
			InitialText:      text,
		})
		if err == nil {
			return session, nil
		}
		if msgType, ok := s.streamingMessageType(); ok {
			fallback, fallbackErr := s.outbound.messenger.BeginStreamingMessageUpdate(ctx, rawfeishu.StreamingMessageRequest{
				ChatID:           s.target.RoomID,
				ReplyToMessageID: s.target.ReplyToMessageID,
				InitialText:      text,
				MsgType:          msgType,
			})
			if fallbackErr == nil {
				return fallback, nil
			}
		}
		return nil, err
	}

	msgType, ok := s.streamingMessageType()
	if ok {
		return s.outbound.messenger.BeginStreamingMessageUpdate(ctx, rawfeishu.StreamingMessageRequest{
			ChatID:           s.target.RoomID,
			ReplyToMessageID: s.target.ReplyToMessageID,
			InitialText:      text,
			MsgType:          msgType,
		})
	}
	return s.outbound.messenger.BeginStreamingCard(ctx, rawfeishu.StreamingCardRequest{
		ChatID:           s.target.RoomID,
		ReplyToMessageID: s.target.ReplyToMessageID,
		InitialText:      text,
	})
}

func (s *feishuStreamingReplySession) shouldUseStreamingCard(text string) bool {
	if s == nil || s.outbound == nil {
		return false
	}
	switch normalizeRenderMode(s.outbound.account.Settings.RenderMode) {
	case "card":
		return s.outbound.account.Settings.Actions.MessageSend
	case "raw":
		return false
	default:
		return s.outbound.account.Settings.Actions.MessageSend && shouldUseFeishuMarkdownCard(text)
	}
}

func (s *feishuStreamingReplySession) streamingMessageType() (string, bool) {
	if s == nil || s.outbound == nil {
		return "", false
	}
	if s.outbound.account.Settings.Actions.PostMessages {
		return "post", true
	}
	if s.outbound.account.Settings.Actions.MessageSend {
		return "text", true
	}
	return "", false
}

func (s *feishuStreamingReplySession) UpdateToolMessage(
	ctx context.Context,
	toolCallID, text string,
	allowEdit bool,
) error {
	if s == nil || s.outbound == nil || s.outbound.messenger == nil {
		return nil
	}
	toolCallID = strings.TrimSpace(toolCallID)
	text = strings.TrimSpace(text)
	if toolCallID == "" || text == "" || !s.toolMessagesEnabled() {
		return nil
	}

	s.mu.Lock()
	if s.toolMessages == nil {
		s.toolMessages = make(map[string]*toolCompanionMessage)
	}
	companion := s.toolMessages[toolCallID]
	s.mu.Unlock()

	if companion == nil {
		created, err := s.startToolMessage(ctx, text)
		if err != nil {
			return err
		}
		s.mu.Lock()
		if s.toolMessages[toolCallID] == nil {
			s.toolMessages[toolCallID] = created
		}
		companion = s.toolMessages[toolCallID]
		s.mu.Unlock()
	}
	if companion == nil || companion.session == nil {
		return nil
	}
	if !allowEdit {
		s.mu.Lock()
		companion.text = text
		s.mu.Unlock()
		return nil
	}
	if err := companion.session.Update(ctx, text); err != nil {
		return err
	}
	s.mu.Lock()
	companion.text = text
	s.mu.Unlock()
	return nil
}

func (s *feishuStreamingReplySession) toolMessagesEnabled() bool {
	if s == nil || s.outbound == nil {
		return false
	}
	return s.outbound.account.Settings.Actions.MessageUpdate &&
		(s.outbound.account.Settings.Actions.MessageSend || s.outbound.account.Settings.Actions.PostMessages)
}

func (s *feishuStreamingReplySession) startToolMessage(
	ctx context.Context,
	text string,
) (*toolCompanionMessage, error) {
	session, err := s.outbound.messenger.BeginStreamingMessageUpdate(ctx, rawfeishu.StreamingMessageRequest{
		ChatID:      s.target.RoomID,
		InitialText: text,
		MsgType:     s.toolMessageType(),
	})
	if err != nil {
		return nil, err
	}
	return &toolCompanionMessage{
		session: session,
		text:    text,
	}, nil
}

func (s *feishuStreamingReplySession) toolMessageType() string {
	if s != nil && s.outbound != nil && s.outbound.account.Settings.Actions.MessageSend {
		return "text"
	}
	return "post"
}

func (s *feishuStreamingReplySession) closeToolMessages(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	messages := make([]*toolCompanionMessage, 0, len(s.toolMessages))
	for _, message := range s.toolMessages {
		if message != nil && message.session != nil {
			messages = append(messages, message)
		}
	}
	s.mu.Unlock()

	for _, message := range messages {
		if err := message.session.Close(ctx, message.text); err != nil {
			return err
		}
	}
	return nil
}

func shouldDelayFeishuAutoStreamingStart(text string, updates int) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return true
	}
	if updates < 2 {
		return true
	}
	if hasFeishuPendingMarkdownLead(text) && updates < 4 {
		return true
	}
	return false
}

func hasFeishuPendingMarkdownLead(text string) bool {
	if text == "" {
		return false
	}
	if strings.Count(text, "```")%2 != 0 {
		return true
	}
	if strings.Contains(text, "\n\n") {
		return true
	}
	firstLine := strings.TrimSpace(strings.SplitN(text, "\n", 2)[0])
	if firstLine == "" {
		return false
	}
	return strings.HasPrefix(firstLine, "#") ||
		strings.HasPrefix(firstLine, ">") ||
		strings.HasPrefix(firstLine, "- ") ||
		strings.HasPrefix(firstLine, "* ")
}

func (o *outbound) BeginProcessingAck(
	ctx context.Context,
	target channelcore.ProcessingAckTarget,
) (channelcore.ProcessingAckHandle, error) {
	if !o.account.Settings.Actions.ProcessingAck {
		return channelcore.ProcessingAckHandle{}, nil
	}
	emoji := strings.TrimSpace(o.account.Settings.ProcessingAckEmoji)
	if emoji == "" {
		return channelcore.ProcessingAckHandle{}, nil
	}
	reaction, err := o.messenger.AddMessageReaction(
		ctx,
		strings.TrimSpace(target.MessageID),
		emoji,
	)
	if err != nil {
		return channelcore.ProcessingAckHandle{}, err
	}
	return channelcore.ProcessingAckHandle{
		MessageID: strings.TrimSpace(target.MessageID),
		AckID:     strings.TrimSpace(reaction.ReactionID),
	}, nil
}

func (o *outbound) EndProcessingAck(
	ctx context.Context,
	handle channelcore.ProcessingAckHandle,
) error {
	_, err := o.messenger.RemoveMessageReaction(
		ctx,
		strings.TrimSpace(handle.MessageID),
		strings.TrimSpace(handle.AckID),
	)
	return err
}

type messageEventIngestor struct {
	profileID domain.ProfileID
	next      channelcore.InboundMessageIngestor
}

func newMessageEventIngestor(
	profileID domain.ProfileID,
	next channelcore.InboundMessageIngestor,
) *messageEventIngestor {
	return &messageEventIngestor{
		profileID: profileID,
		next:      next,
	}
}

func (i *messageEventIngestor) IngestFeishuMessageEvent(
	ctx context.Context,
	_ domain.ProfileID,
	event rawfeishu.MessageEvent,
) error {
	if i.next == nil {
		return fmt.Errorf("feishu channel: missing inbound ingestor")
	}

	message, err := rawfeishu.NormalizeMessageEvent(i.profileID, event)
	if err != nil {
		return err
	}

	return i.next.IngestInboundMessage(ctx, channelcore.InboundMessage{
		Provider:          domain.ProviderFeishu,
		ProfileID:         message.ProfileID,
		RoomID:            message.RoomID,
		RoomKind:          message.RoomKind,
		ProviderRoomID:    string(message.RoomID),
		PersonID:          message.PersonID,
		ProviderUserID:    string(message.PersonID),
		ProviderMessageID: message.ProviderMessageID,
		RoomName:          message.RoomName,
		PersonName:        message.PersonName,
		ContentText:       message.ContentText,
		ReceivedAt:        message.ReceivedAt,
		ChatType:          string(message.ChatType),
		ContentType:       string(message.ContentType),
		RootID:            message.RootID,
		ParentID:          message.ParentID,
		ThreadID:          message.ThreadID,
	})
}

type eventObserver struct {
	profileID domain.ProfileID
	next      channelcore.ChannelEventObserver
}

func newEventObserver(
	profileID domain.ProfileID,
	next channelcore.ChannelEventObserver,
) *eventObserver {
	return &eventObserver{
		profileID: profileID,
		next:      next,
	}
}

func (o *eventObserver) ObserveFeishuEvent(
	ctx context.Context,
	_ domain.ProfileID,
	event rawfeishu.Event,
) error {
	if o.next == nil {
		return nil
	}

	providerUserID, personID := normalizeActor(event)
	return o.next.ObserveChannelEvent(ctx, channelcore.ChannelEvent{
		Provider:          domain.ProviderFeishu,
		ProfileID:         o.profileID,
		EventID:           strings.TrimSpace(event.EventID),
		Type:              strings.TrimSpace(event.EventType),
		RoomID:            domain.RoomID(strings.TrimSpace(event.ChatID)),
		ProviderRoomID:    strings.TrimSpace(event.ChatID),
		PersonID:          personID,
		ProviderUserID:    providerUserID,
		ProviderMessageID: strings.TrimSpace(event.MessageID),
		OccurredAt:        event.OccurredAt,
		PayloadJSON:       clonePayload(event.Payload),
	})
}

func normalizeActor(event rawfeishu.Event) (string, domain.PersonID) {
	if openID := strings.TrimSpace(event.Actor.OpenID); openID != "" {
		return openID, domain.PersonID(openID)
	}
	if userID := strings.TrimSpace(event.Actor.UserID); userID != "" {
		return userID, ""
	}
	if unionID := strings.TrimSpace(event.Actor.UnionID); unionID != "" {
		return unionID, ""
	}
	if appID := strings.TrimSpace(event.ActorAppID); appID != "" {
		return "app:" + appID, ""
	}
	return "", ""
}

func clonePayload(payload []byte) []byte {
	if len(payload) == 0 {
		return nil
	}
	out := make([]byte, len(payload))
	copy(out, payload)
	return out
}

type runnerTransport struct {
	name   string
	logger *slog.Logger
	run    func(context.Context) error
}

func (t runnerTransport) Name() string {
	return t.name
}

func (t runnerTransport) Run(ctx context.Context) error {
	if t.logger != nil {
		t.logger.Info("channel transport: starting runner", "transport", t.name)
	}
	err := t.run(ctx)
	if err == nil && t.logger != nil {
		t.logger.Info("channel transport: runner exited", "transport", t.name)
	}
	return err
}

func resolveAccounts(cfg config.FeishuConfig) []resolvedAccount {
	accountIDs := listAccountIDs(cfg)
	accounts := make([]resolvedAccount, 0, len(accountIDs))
	for _, accountID := range accountIDs {
		accounts = append(accounts, resolveAccount(cfg, accountID))
	}
	return accounts
}

func listAccountIDs(cfg config.FeishuConfig) []string {
	if len(cfg.Accounts) == 0 {
		return []string{resolveDefaultAccountID(cfg)}
	}

	ids := make([]string, 0, len(cfg.Accounts))
	for accountID := range cfg.Accounts {
		accountID = strings.TrimSpace(accountID)
		if accountID == "" {
			continue
		}
		ids = append(ids, accountID)
	}
	slices.Sort(ids)
	if len(ids) == 0 {
		return []string{resolveDefaultAccountID(cfg)}
	}
	return ids
}

func resolveDefaultAccountID(cfg config.FeishuConfig) string {
	defaultAccountID := strings.TrimSpace(cfg.DefaultAccount)
	if defaultAccountID == "" {
		return DefaultAccountID
	}
	return defaultAccountID
}

func resolveAccount(cfg config.FeishuConfig, accountID string) resolvedAccount {
	trimmedAccountID := strings.TrimSpace(accountID)
	if trimmedAccountID == "" {
		trimmedAccountID = resolveDefaultAccountID(cfg)
	}

	settings := accountSettings{
		RenderMode:             normalizeRenderMode(cfg.RenderMode),
		StreamingToolSummaries: normalizeStreamingToolSummaries(cfg.StreamingToolSummaries),
		Actions:                cfg.Actions,
		ProcessingAckEmoji:     strings.TrimSpace(cfg.ProcessingAckEmoji),
		ProfileID:              domain.ProfileID(strings.TrimSpace(cfg.ProfileID)),
		ConnectionMode:         normalizeConnectionMode(cfg.ConnectionMode),
		WebhookHost:            strings.TrimSpace(cfg.WebhookHost),
		WebhookPort:            cfg.WebhookPort,
		WebhookPath:            strings.TrimSpace(cfg.WebhookPath),
		APIBaseURL:             strings.TrimSpace(cfg.APIBaseURL),
		AppID:                  strings.TrimSpace(cfg.AppID),
		AppSecret:              strings.TrimSpace(cfg.AppSecret),
		EncryptKey:             strings.TrimSpace(cfg.EncryptKey),
		VerificationToken:      strings.TrimSpace(cfg.VerificationToken),
		MaxBodyBytes:           cfg.MaxBodyBytes,
	}
	enabled := cfg.Enabled

	if override, ok := cfg.Accounts[trimmedAccountID]; ok {
		if override.Enabled != nil {
			enabled = enabled && *override.Enabled
		}
		if override.Name != "" {
			settings.Name = strings.TrimSpace(override.Name)
		}
		if override.RenderMode != "" {
			settings.RenderMode = normalizeRenderMode(override.RenderMode)
		}
		if override.StreamingToolSummaries != "" {
			settings.StreamingToolSummaries = normalizeStreamingToolSummaries(override.StreamingToolSummaries)
		}
		if override.Actions.ProcessingAck != nil {
			settings.Actions.ProcessingAck = *override.Actions.ProcessingAck
		}
		if override.Actions.MessageSend != nil {
			settings.Actions.MessageSend = *override.Actions.MessageSend
		}
		if override.Actions.MessageUpdate != nil {
			settings.Actions.MessageUpdate = *override.Actions.MessageUpdate
		}
		if override.Actions.MessageRecall != nil {
			settings.Actions.MessageRecall = *override.Actions.MessageRecall
		}
		if override.Actions.Reactions != nil {
			settings.Actions.Reactions = *override.Actions.Reactions
		}
		if override.Actions.Pins != nil {
			settings.Actions.Pins = *override.Actions.Pins
		}
		if override.Actions.PostMessages != nil {
			settings.Actions.PostMessages = *override.Actions.PostMessages
		}
		if override.Actions.DocsRead != nil {
			settings.Actions.DocsRead = *override.Actions.DocsRead
		}
		if override.ProcessingAckEmoji != "" {
			settings.ProcessingAckEmoji = strings.TrimSpace(override.ProcessingAckEmoji)
		}
		if override.ProfileID != "" {
			settings.ProfileID = domain.ProfileID(strings.TrimSpace(override.ProfileID))
		}
		if override.ConnectionMode != "" {
			settings.ConnectionMode = normalizeConnectionMode(override.ConnectionMode)
		}
		if override.WebhookHost != "" {
			settings.WebhookHost = strings.TrimSpace(override.WebhookHost)
		}
		if override.WebhookPort > 0 {
			settings.WebhookPort = override.WebhookPort
		}
		if override.WebhookPath != "" {
			settings.WebhookPath = strings.TrimSpace(override.WebhookPath)
		}
		if override.APIBaseURL != "" {
			settings.APIBaseURL = strings.TrimSpace(override.APIBaseURL)
		}
		if override.AppID != "" {
			settings.AppID = strings.TrimSpace(override.AppID)
		}
		if override.AppSecret != "" {
			settings.AppSecret = strings.TrimSpace(override.AppSecret)
		}
		if override.EncryptKey != "" {
			settings.EncryptKey = strings.TrimSpace(override.EncryptKey)
		}
		if override.VerificationToken != "" {
			settings.VerificationToken = strings.TrimSpace(override.VerificationToken)
		}
		if override.MaxBodyBytes > 0 {
			settings.MaxBodyBytes = override.MaxBodyBytes
		}
	}

	if settings.ConnectionMode == "" {
		settings.ConnectionMode = "webhook"
	}
	if settings.RenderMode == "" {
		settings.RenderMode = normalizeRenderMode(config.Default().Channels.Feishu.RenderMode)
	}
	if settings.WebhookHost == "" {
		settings.WebhookHost = "127.0.0.1"
	}
	if settings.WebhookPort <= 0 {
		settings.WebhookPort = 3000
	}
	if settings.WebhookPath == "" {
		settings.WebhookPath = "/feishu/events"
	}
	if settings.APIBaseURL == "" {
		settings.APIBaseURL = "https://open.feishu.cn/open-apis"
	}
	if settings.ProcessingAckEmoji == "" {
		settings.ProcessingAckEmoji = config.Default().Channels.Feishu.ProcessingAckEmoji
	}
	if settings.MaxBodyBytes <= 0 {
		settings.MaxBodyBytes = 1 << 20
	}

	return resolvedAccount{
		AccountID:  trimmedAccountID,
		Enabled:    enabled,
		Configured: settings.ProfileID != "" && settings.AppID != "" && settings.AppSecret != "",
		Settings:   settings,
		TransportID: fmt.Sprintf(
			"feishu[%s:%s]",
			trimmedAccountID,
			settings.ConnectionMode,
		),
	}
}

func normalizeConnectionMode(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "longpoll", "websocket":
		return "longpoll"
	case "webhook":
		return "webhook"
	default:
		return "webhook"
	}
}

func normalizeRenderMode(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "card":
		return "card"
	case "raw":
		return "raw"
	default:
		return "auto"
	}
}

func normalizeStreamingToolSummaries(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "all":
		return "all"
	case "dm_only":
		return "dm_only"
	default:
		return "off"
	}
}

func isLongpollMode(value string) bool {
	return normalizeConnectionMode(value) == "longpoll"
}

func isWebhookMode(value string) bool {
	return normalizeConnectionMode(value) == "webhook"
}
