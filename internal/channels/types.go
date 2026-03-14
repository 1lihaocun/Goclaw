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

type StreamingReplyTarget struct {
	RoomID           string
	ReplyToMessageID string
}

type Outbound interface {
	ProfileID() domain.ProfileID
	Provider() domain.Provider
	Configured() bool
	SendText(ctx context.Context, roomID, text string) (SendResult, error)
}

type StreamingReplySession interface {
	SendResult() SendResult
	UpdateText(ctx context.Context, text string) error
	Close(ctx context.Context, finalText string) error
}

type ToolStreamingReplySession interface {
	StreamingReplySession
	UpdateToolMessage(ctx context.Context, toolCallID, text string, allowEdit bool) error
}

type StreamingReplyOutbound interface {
	Outbound
	BeginStreamingReply(ctx context.Context, target StreamingReplyTarget) (StreamingReplySession, error)
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
	Logger                 *slog.Logger
	Ingestor               InboundMessageIngestor
	EventObserver          ChannelEventObserver
	RootContext            context.Context
	ManagedRuntimePolicy   ManagedRuntimePolicy
	ManagedRuntimeObserver ManagedRuntimeObserver
}

type ManagedRuntimePolicy struct {
	Enabled        bool
	MaxAttempts    int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

type ManagedRuntimeObserver interface {
	MarkRuntimeStarting(runtimeID string, now time.Time)
	MarkRuntimeBackingOff(runtimeID string, now time.Time, nextRetryAt time.Time, err error)
	MarkRuntimeStopped(runtimeID string, now time.Time, err error)
	ReportRuntimeFailure(runtimeID string, err error)
}

type AccountSnapshot struct {
	Provider        domain.Provider  `json:"provider"`
	AccountID       string           `json:"account_id"`
	Name            string           `json:"name,omitempty"`
	ProfileID       domain.ProfileID `json:"profile_id"`
	Enabled         bool             `json:"enabled"`
	Configured      bool             `json:"configured"`
	ConnectionMode  string           `json:"connection_mode,omitempty"`
	TransportID     string           `json:"transport_id,omitempty"`
	RuntimeID       string           `json:"runtime_id,omitempty"`
	WebhookAddress  string           `json:"webhook_address,omitempty"`
	WebhookPath     string           `json:"webhook_path,omitempty"`
	BackgroundOnly  bool             `json:"background_only,omitempty"`
	SupportsIngress bool             `json:"supports_ingress,omitempty"`
}

type TransportRuntimeKind string

const (
	TransportRuntimeKindWebhook  TransportRuntimeKind = "webhook"
	TransportRuntimeKindLongpoll TransportRuntimeKind = "longpoll"
)

type TransportLifecycleMode string

const (
	TransportLifecycleModeSharedRuntime    TransportLifecycleMode = "shared_runtime"
	TransportLifecycleModeDedicatedRuntime TransportLifecycleMode = "dedicated_runtime"
)

type AccountRef struct {
	Provider  domain.Provider  `json:"provider"`
	AccountID string           `json:"account_id"`
	ProfileID domain.ProfileID `json:"profile_id"`
}

type TransportBinding struct {
	AccountID      string           `json:"account_id"`
	ProfileID      domain.ProfileID `json:"profile_id"`
	TransportID    string           `json:"transport_id"`
	ConnectionMode string           `json:"connection_mode"`
	WebhookPath    string           `json:"webhook_path,omitempty"`
	Configured     bool             `json:"configured"`
}

type TransportSnapshot struct {
	Provider        domain.Provider        `json:"provider"`
	Name            string                 `json:"name"`
	RuntimeID       string                 `json:"runtime_id"`
	Kind            TransportRuntimeKind   `json:"kind"`
	LifecycleMode   TransportLifecycleMode `json:"lifecycle_mode"`
	OperatorManaged bool                   `json:"operator_managed"`
	Shared          bool                   `json:"shared"`
	Address         string                 `json:"address,omitempty"`
	Bindings        []TransportBinding     `json:"bindings,omitempty"`
}

type AccountLifecycleSpec struct {
	Provider        domain.Provider        `json:"provider"`
	AccountID       string                 `json:"account_id"`
	ProfileID       domain.ProfileID       `json:"profile_id"`
	Enabled         bool                   `json:"enabled"`
	Configured      bool                   `json:"configured"`
	RuntimeID       string                 `json:"runtime_id,omitempty"`
	ConnectionMode  string                 `json:"connection_mode,omitempty"`
	LifecycleMode   TransportLifecycleMode `json:"lifecycle_mode,omitempty"`
	OperatorManaged bool                   `json:"operator_managed"`
	SupportsStart   bool                   `json:"supports_start"`
	SupportsStop    bool                   `json:"supports_stop"`
	SupportsRestart bool                   `json:"supports_restart"`
	Reason          string                 `json:"reason,omitempty"`
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

type AccountSnapshotReporter interface {
	AccountSnapshots() []AccountSnapshot
}

type TransportSnapshotReporter interface {
	TransportSnapshots() []TransportSnapshot
}

type AccountLifecycleSpecReporter interface {
	AccountLifecycleSpecs() []AccountLifecycleSpec
}

type AccountLifecycleController interface {
	StartAccount(ctx context.Context, account AccountRef) error
	StopAccount(ctx context.Context, account AccountRef) error
	RestartAccount(ctx context.Context, account AccountRef) error
}

type RuntimeBinder interface {
	BindRuntime(params BuildRuntimeParams)
}
