package agenttools

import (
	"encoding/json"
	"strings"

	"goclaw/internal/domain"
)

type AuditDecision struct {
	Allowed      bool            `json:"allowed"`
	Reason       string          `json:"reason,omitempty"`
	Source       Source          `json:"source,omitempty"`
	Provider     domain.Provider `json:"provider,omitempty"`
	SafetyClass  SafetyClass     `json:"safety_class,omitempty"`
	CapabilityID string          `json:"capability_id,omitempty"`
}

func MarshalAuditDecision(allowed bool, definition Definition, reason string) string {
	payload := AuditDecision{
		Allowed:      allowed,
		Reason:       strings.TrimSpace(reason),
		Source:       definition.Source,
		Provider:     definition.Provider,
		SafetyClass:  definition.SafetyClass,
		CapabilityID: strings.TrimSpace(definition.CapabilityID),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "{}"
	}
	return string(data)
}
