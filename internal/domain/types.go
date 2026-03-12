package domain

import "time"

type ProfileID string
type PersonID string
type RoomID string
type ConversationID string
type SessionID string
type MessageID string

type Provider string

const (
	ProviderFeishu Provider = "feishu"
)

type ConsentAccessLevel string

const (
	ConsentDeny    ConsentAccessLevel = "deny"
	ConsentSummary ConsentAccessLevel = "summary"
	ConsentFull    ConsentAccessLevel = "full"
)

type RoomKind string

const (
	RoomKindGroup  RoomKind = "group"
	RoomKindDirect RoomKind = "direct"
)

type MessageRole string

const (
	MessageRoleUser      MessageRole = "user"
	MessageRoleAssistant MessageRole = "assistant"
	MessageRoleSystem    MessageRole = "system"
)

type Profile struct {
	ID   ProfileID
	Name string
}

type Person struct {
	ID             PersonID
	Provider       Provider
	ProviderUserID string
	DisplayName    string
}

type Room struct {
	ID             RoomID
	Provider       Provider
	ProviderRoomID string
	Kind           RoomKind
	Name           string
}

type RoomSession struct {
	ID                   SessionID
	ProfileID            ProfileID
	RoomID               RoomID
	Status               string
	SummaryText          string
	ActiveConversationID ConversationID
	LastMessageAt        time.Time
}

type ReplyRunStatus string

const (
	ReplyRunRunning   ReplyRunStatus = "running"
	ReplyRunCompleted ReplyRunStatus = "completed"
	ReplyRunFailed    ReplyRunStatus = "failed"
)

type ReplyRun struct {
	ID               string
	ProfileID        ProfileID
	RoomID           RoomID
	ConversationID   ConversationID
	SessionID        SessionID
	InboundMessageID MessageID
	ModelProvider    string
	ToolingMode      string
	Status           ReplyRunStatus
	ErrorText        string
	StartedAt        time.Time
	FinishedAt       *time.Time
}

type ToolInvocationStatus string

const (
	ToolInvocationRunning   ToolInvocationStatus = "running"
	ToolInvocationCompleted ToolInvocationStatus = "completed"
	ToolInvocationFailed    ToolInvocationStatus = "failed"
)

type ToolInvocation struct {
	ID           string
	ReplyRunID   string
	Iteration    int
	ToolCallID   string
	ToolName     string
	ToolSource   string
	Provider     Provider
	CapabilityID string
	ArgsJSON     string
	DecisionJSON string
	Status       ToolInvocationStatus
	ResultJSON   string
	ErrorText    string
	StartedAt    time.Time
	FinishedAt   *time.Time
}

type ConversationStatus string

const (
	ConversationStatusActive ConversationStatus = "active"
)

const DefaultConversationSlug = "default"

type Conversation struct {
	ID        ConversationID
	ProfileID ProfileID
	RoomID    RoomID
	Slug      string
	Title     string
	Status    ConversationStatus
	CreatedAt time.Time
	UpdatedAt time.Time
}

type ConversationJournalMode string

const (
	ConversationJournalOff          ConversationJournalMode = "off"
	ConversationJournalProviderOnly ConversationJournalMode = "provider_only"
	ConversationJournalMarkdownOnly ConversationJournalMode = "markdown_only"
	ConversationJournalBoth         ConversationJournalMode = "both"
)

const (
	ConversationScopeConversation  = "conversation"
	ConversationScopeRoom          = "room"
	ConversationScopePersonSummary = "person_summary"
	ConversationScopePersonPrivate = "person_private"
)

type ConversationSettings struct {
	ConversationID                ConversationID
	Enabled                       bool
	AllowProviderMemory           bool
	AllowMarkdownMemory           bool
	ReadScopes                    []string
	WriteScopes                   []string
	WriteAssistantToPersonPrivate bool
	JournalMode                   ConversationJournalMode
	Explicit                      bool
	UpdatedBy                     string
	CreatedAt                     time.Time
	UpdatedAt                     time.Time
}

type ConsentPolicy struct {
	ProfileID   ProfileID
	RoomID      RoomID
	PersonID    PersonID
	AccessLevel ConsentAccessLevel
	ExpiresAt   *time.Time
	Explicit    bool
	UpdatedBy   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type Message struct {
	ID                MessageID
	ProfileID         ProfileID
	RoomID            RoomID
	ConversationID    ConversationID
	PersonID          PersonID
	SessionID         SessionID
	ProviderMessageID string
	Role              MessageRole
	ContentText       string
	RawJSON           string
	CreatedAt         time.Time
}

type ChannelEvent struct {
	ID                string
	Provider          Provider
	ProfileID         ProfileID
	EventID           string
	Type              string
	RoomID            RoomID
	ProviderRoomID    string
	PersonID          PersonID
	ProviderUserID    string
	ProviderMessageID string
	PayloadJSON       string
	OccurredAt        time.Time
	CreatedAt         time.Time
}

type MemoryJobStatus string

const (
	MemoryJobPending    MemoryJobStatus = "pending"
	MemoryJobProcessing MemoryJobStatus = "processing"
	MemoryJobReview     MemoryJobStatus = "review_pending"
	MemoryJobDone       MemoryJobStatus = "done"
	MemoryJobRejected   MemoryJobStatus = "rejected"
	MemoryJobFailed     MemoryJobStatus = "failed"
)

type MemoryJob struct {
	ID              string
	Kind            string
	TargetType      string
	TargetID        string
	SourceMessageID MessageID
	PayloadJSON     string
	Status          MemoryJobStatus
	ErrorText       string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type ToolPermissionMode string

const (
	ToolPermissionDenyAll   ToolPermissionMode = "deny_all"
	ToolPermissionAllowAll  ToolPermissionMode = "allow_all"
	ToolPermissionAllowList ToolPermissionMode = "allow_list"
)

type ToolPermissionPolicy struct {
	ProfileID                ProfileID
	RoomID                   RoomID
	CommandsMode             ToolPermissionMode
	PathsMode                ToolPermissionMode
	NetworkMode              ToolPermissionMode
	ChannelIntrospectionMode ToolPermissionMode
	ChannelReadMode          ToolPermissionMode
	ChannelWriteMode         ToolPermissionMode
	ChannelSensitiveMode     ToolPermissionMode
	AllowedCommands          []string
	DeniedCommands           []string
	AllowedPaths             []string
	DeniedPaths              []string
	AllowedHosts             []string
	DeniedHosts              []string
	AllowedChannelTools      []string
	DeniedChannelTools       []string
	AllowedChannelProviders  []string
	DeniedChannelProviders   []string
	Explicit                 bool
	UpdatedBy                string
	CreatedAt                time.Time
	UpdatedAt                time.Time
}

func DefaultConversationSettings(conversationID ConversationID) ConversationSettings {
	return ConversationSettings{
		ConversationID:                conversationID,
		Enabled:                       true,
		AllowProviderMemory:           true,
		AllowMarkdownMemory:           true,
		ReadScopes:                    defaultConversationScopes(),
		WriteScopes:                   defaultConversationScopes(),
		WriteAssistantToPersonPrivate: true,
		JournalMode:                   ConversationJournalProviderOnly,
	}
}

func defaultConversationScopes() []string {
	return []string{
		ConversationScopeConversation,
		ConversationScopeRoom,
		ConversationScopePersonSummary,
		ConversationScopePersonPrivate,
	}
}
