package agenttools

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"goclaw/internal/domain"
	"goclaw/internal/model"
	sqlitestore "goclaw/internal/store/sqlite"
)

type Source string

const (
	SourceLocal   Source = "local"
	SourceChannel Source = "channel"
)

type SafetyClass string

const (
	SafetyIntrospection SafetyClass = "introspection"
	SafetyReadOnly      SafetyClass = "read_only"
	SafetyMutating      SafetyClass = "mutating"
	SafetySensitive     SafetyClass = "sensitive"
)

type ProfileBindingMode string

const (
	ProfileBindingCurrentSession  ProfileBindingMode = "current_session"
	ProfileBindingExplicitProfile ProfileBindingMode = "explicit_profile"
	ProfileBindingAccountOptional ProfileBindingMode = "account_optional"
)

type SessionContext struct {
	Provider                 domain.Provider
	ProfileID                domain.ProfileID
	RoomID                   domain.RoomID
	ProviderRoomID           string
	RoomKind                 domain.RoomKind
	PersonID                 domain.PersonID
	ProviderUserID           string
	ConversationID           domain.ConversationID
	CurrentMessageProviderID string
}

type AccountMetadata struct {
	Provider       domain.Provider
	AccountID      string
	Name           string
	ProfileID      domain.ProfileID
	Enabled        bool
	Configured     bool
	ConnectionMode string
}

type ProviderMetadata struct {
	RoomKind                 domain.RoomKind
	ProviderRoomID           string
	ProviderUserID           string
	CurrentMessageProviderID string
}

type ExecutionContext struct {
	Session          SessionContext
	Account          AccountMetadata
	ProviderMetadata ProviderMetadata
	Logger           *slog.Logger
	Repos            sqlitestore.Repositories
	Now              func() time.Time
}

type Definition struct {
	Name               string
	Description        string
	InputSchema        json.RawMessage
	Source             Source
	Provider           domain.Provider
	ProfileBindingMode ProfileBindingMode
	Account            AccountMetadata
	ProviderMetadata   ProviderMetadata
	SafetyClass        SafetyClass
	CapabilityID       string
	Execute            func(context.Context, ExecutionContext, json.RawMessage) (any, error)
}

func (d Definition) ModelTool() model.Tool {
	return model.Tool{
		Name:        d.Name,
		Description: d.Description,
		InputSchema: append(json.RawMessage(nil), d.InputSchema...),
	}
}

type Provider interface {
	VisibleTools(session SessionContext, policy domain.ToolPermissionPolicy) []Definition
	CapabilitySummary(
		context.Context,
		SessionContext,
		domain.ToolPermissionPolicy,
	) (CapabilitySummary, bool, error)
}

type CapabilitySummary struct {
	ScopeSummary string
}
