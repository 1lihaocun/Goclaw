package feishu

import (
	"time"

	"goclaw/internal/domain"
)

// InboundMessage is the normalized message shape GoClaw should consume from
// Feishu adapters and webhook handlers.
type InboundMessage struct {
	ProfileID         domain.ProfileID
	RoomID            domain.RoomID
	RoomKind          domain.RoomKind
	PersonID          domain.PersonID
	ProviderMessageID string
	RoomName          string
	PersonName        string
	ContentText       string
	ReceivedAt        time.Time
	ChatType          ChatType
	ContentType       MessageType
	RootID            string
	ParentID          string
	ThreadID          string
}
