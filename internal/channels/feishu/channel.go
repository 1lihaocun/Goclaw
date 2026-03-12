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
	Name               string
	RenderMode         string
	Actions            config.FeishuActionsConfig
	ProcessingAckEmoji string
	ProfileID          domain.ProfileID
	ConnectionMode     string
	WebhookHost        string
	WebhookPort        int
	WebhookPath        string
	APIBaseURL         string
	AppID              string
	AppSecret          string
	EncryptKey         string
	VerificationToken  string
	MaxBodyBytes       int64
}

type resolvedAccount struct {
	AccountID   string
	Enabled     bool
	Configured  bool
	Settings    accountSettings
	TransportID string
}

type Channel struct {
	config    config.FeishuConfig
	accounts  []resolvedAccount
	outbounds []channelcore.Outbound
	logger    *slog.Logger
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
		config:    cfg,
		accounts:  accounts,
		outbounds: outbounds,
		logger:    logger,
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

		runner, err := rawlongpoll.New(rawlongpoll.Params{
			ProfileID:     account.Settings.ProfileID,
			AppID:         account.Settings.AppID,
			AppSecret:     account.Settings.AppSecret,
			BaseURL:       account.Settings.APIBaseURL,
			Ingestor:      newMessageEventIngestor(account.Settings.ProfileID, params.Ingestor),
			EventObserver: newEventObserver(account.Settings.ProfileID, params.EventObserver),
			Logger:        params.Logger,
		})
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

func shouldUseFeishuMarkdownCard(text string) bool {
	return feishuFencedCodeBlockPattern.MatchString(text) || feishuMarkdownTablePattern.MatchString(text)
}

func toChannelSendResult(result rawfeishu.SendResult) channelcore.SendResult {
	return channelcore.SendResult{
		MessageID: result.MessageID,
		RoomID:    result.ChatID,
	}
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
		RenderMode:         normalizeRenderMode(cfg.RenderMode),
		Actions:            cfg.Actions,
		ProcessingAckEmoji: strings.TrimSpace(cfg.ProcessingAckEmoji),
		ProfileID:          domain.ProfileID(strings.TrimSpace(cfg.ProfileID)),
		ConnectionMode:     normalizeConnectionMode(cfg.ConnectionMode),
		WebhookHost:        strings.TrimSpace(cfg.WebhookHost),
		WebhookPort:        cfg.WebhookPort,
		WebhookPath:        strings.TrimSpace(cfg.WebhookPath),
		APIBaseURL:         strings.TrimSpace(cfg.APIBaseURL),
		AppID:              strings.TrimSpace(cfg.AppID),
		AppSecret:          strings.TrimSpace(cfg.AppSecret),
		EncryptKey:         strings.TrimSpace(cfg.EncryptKey),
		VerificationToken:  strings.TrimSpace(cfg.VerificationToken),
		MaxBodyBytes:       cfg.MaxBodyBytes,
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

func isLongpollMode(value string) bool {
	return normalizeConnectionMode(value) == "longpoll"
}

func isWebhookMode(value string) bool {
	return normalizeConnectionMode(value) == "webhook"
}
