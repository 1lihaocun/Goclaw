package longpoll

import (
	"context"
	"log/slog"
	"net/url"
	"strings"
	"time"

	larkdispatcher "github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"

	"goclaw/internal/domain"
	"goclaw/internal/feishu"
)

type MessageEventIngestor interface {
	IngestFeishuMessageEvent(ctx context.Context, profileID domain.ProfileID, event feishu.MessageEvent) error
}

type EventObserver interface {
	ObserveFeishuEvent(ctx context.Context, profileID domain.ProfileID, event feishu.Event) error
}

type wsClient interface {
	Start(ctx context.Context) error
}

type wsClientFactory func(params Params, handler *larkdispatcher.EventDispatcher) wsClient

type Params struct {
	ProfileID     domain.ProfileID
	AppID         string
	AppSecret     string
	BaseURL       string
	Ingestor      MessageEventIngestor
	EventObserver EventObserver
	Logger        *slog.Logger

	clientFactory wsClientFactory
	waitBackoff   func(context.Context, time.Duration) error
}

func (p Params) withDefaults() Params {
	if p.Logger == nil {
		p.Logger = slog.Default()
	}
	if p.clientFactory == nil {
		p.clientFactory = defaultWSClientFactory
	}
	if p.waitBackoff == nil {
		p.waitBackoff = waitBackoff
	}
	return p
}

func waitBackoff(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type sdkWSClient struct {
	client *larkws.Client
}

func (c *sdkWSClient) Start(ctx context.Context) error {
	return c.client.Start(ctx)
}

func defaultWSClientFactory(params Params, handler *larkdispatcher.EventDispatcher) wsClient {
	handler.Config.Logger = newSlogLarkLogger(params.Logger)
	options := []larkws.ClientOption{
		larkws.WithEventHandler(handler),
		larkws.WithLogger(newSlogLarkLogger(params.Logger)),
	}

	if domain := resolveWSBaseURL(params.BaseURL); domain != "" {
		options = append(options, larkws.WithDomain(domain))
	}

	return &sdkWSClient{
		client: larkws.NewClient(params.AppID, params.AppSecret, options...),
	}
}

func resolveWSBaseURL(baseURL string) string {
	trimmed := strings.TrimSpace(baseURL)
	if trimmed == "" {
		return ""
	}

	parsed, err := url.Parse(trimmed)
	if err == nil && parsed.Scheme != "" && parsed.Host != "" {
		return parsed.Scheme + "://" + parsed.Host
	}

	trimmed = strings.TrimRight(trimmed, "/")
	return strings.TrimSuffix(trimmed, "/open-apis")
}
