package channels

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"goclaw/internal/agenttools"
	"goclaw/internal/domain"
	sqlitestore "goclaw/internal/store/sqlite"
)

type InboundMessage struct {
	Provider  domain.Provider
	ProfileID domain.ProfileID
	// RoomID and PersonID are canonical storage identifiers owned by the
	// channel implementation. ProviderRoomID and ProviderUserID keep the raw
	// upstream identifiers.
	RoomID            domain.RoomID
	RoomKind          domain.RoomKind
	ProviderRoomID    string
	PersonID          domain.PersonID
	ProviderUserID    string
	ProviderMessageID string
	RoomName          string
	PersonName        string
	ContentText       string
	ReceivedAt        time.Time
	ChatType          string
	ContentType       string
	RootID            string
	ParentID          string
	ThreadID          string
}

type ChannelEvent struct {
	Provider          domain.Provider
	ProfileID         domain.ProfileID
	EventID           string
	Type              string
	RoomID            domain.RoomID
	ProviderRoomID    string
	PersonID          domain.PersonID
	ProviderUserID    string
	ProviderMessageID string
	OccurredAt        time.Time
	PayloadJSON       []byte
}

type SendResult struct {
	MessageID string
	RoomID    string
}

type ProcessingAckTarget struct {
	RoomID    string
	MessageID string
}

type ProcessingAckHandle struct {
	MessageID string
	AckID     string
}

type Outbound interface {
	ProfileID() domain.ProfileID
	Provider() domain.Provider
	Configured() bool
	SendText(ctx context.Context, roomID, text string) (SendResult, error)
}

type ProcessingAckOutbound interface {
	Outbound
	BeginProcessingAck(ctx context.Context, target ProcessingAckTarget) (ProcessingAckHandle, error)
	EndProcessingAck(ctx context.Context, handle ProcessingAckHandle) error
}

type InboundMessageIngestor interface {
	IngestInboundMessage(ctx context.Context, message InboundMessage) error
}

type ChannelEventObserver interface {
	ObserveChannelEvent(ctx context.Context, event ChannelEvent) error
}

type BuildRuntimeParams struct {
	Logger        *slog.Logger
	Ingestor      InboundMessageIngestor
	EventObserver ChannelEventObserver
}

type ToolBuildParams struct {
	Logger *slog.Logger
	Repos  sqlitestore.Repositories
	Now    func() time.Time
}

type HTTPRoute struct {
	Name    string
	Address string
	Path    string
	Handler http.Handler
}

type Transport interface {
	Name() string
	Run(ctx context.Context) error
}

type Channel interface {
	Provider() domain.Provider
	Outbounds() []Outbound
	HTTPRoutes(params BuildRuntimeParams) ([]HTTPRoute, error)
	Transports(params BuildRuntimeParams) ([]Transport, error)
	AgentToolProviders(params ToolBuildParams) ([]agenttools.Provider, error)
}
