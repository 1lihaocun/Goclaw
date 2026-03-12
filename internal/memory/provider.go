package memory

import (
	"context"
	"time"

	"goclaw/internal/domain"
)

type ScopeKind string

const (
	ScopeConversation  ScopeKind = "conversation"
	ScopeRoom          ScopeKind = "room"
	ScopePersonSummary ScopeKind = "person_summary"
	ScopePersonPrivate ScopeKind = "person_private"
)

type Scope struct {
	Kind           ScopeKind
	ProfileID      domain.ProfileID
	RoomID         domain.RoomID
	ConversationID domain.ConversationID
	PersonID       domain.PersonID
}

type SearchQuery struct {
	Text  string
	Limit int
}

type SearchResult struct {
	ID       string
	Content  string
	Source   string
	Score    float64
	Metadata map[string]string
}

type WriteRecord struct {
	ID        string
	Content   string
	Metadata  map[string]string
	CreatedAt time.Time
}

type HealthStatus struct {
	Available bool
	Detail    string
}

type Provider interface {
	Name() string
	Search(context.Context, Scope, SearchQuery) ([]SearchResult, error)
	Write(context.Context, Scope, []WriteRecord) error
	Health(context.Context) HealthStatus
}
