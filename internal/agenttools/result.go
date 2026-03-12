package agenttools

import (
	"encoding/json"
	"errors"
	"strings"

	"goclaw/internal/domain"
)

type ErrorKind string

const (
	ErrorPermissionDenied     ErrorKind = "permission_denied"
	ErrorInvalidInput         ErrorKind = "invalid_input"
	ErrorNotFound             ErrorKind = "not_found"
	ErrorRateLimited          ErrorKind = "rate_limited"
	ErrorTemporaryUnavailable ErrorKind = "temporary_unavailable"
	ErrorProvider             ErrorKind = "provider_error"
)

type ErrorEnvelope struct {
	Kind       ErrorKind       `json:"kind"`
	Provider   domain.Provider `json:"provider,omitempty"`
	Tool       string          `json:"tool,omitempty"`
	Message    string          `json:"message"`
	Retryable  bool            `json:"retryable,omitempty"`
	GrantURL   string          `json:"grant_url,omitempty"`
	Details    any             `json:"details,omitempty"`
	Capability string          `json:"capability_id,omitempty"`
}

type ResultEnvelope struct {
	OK     bool           `json:"ok"`
	Tool   string         `json:"tool"`
	Result any            `json:"result,omitempty"`
	Error  string         `json:"error,omitempty"`
	Meta   *ErrorEnvelope `json:"meta,omitempty"`
}

type ExecutionError struct {
	Kind       ErrorKind
	Provider   domain.Provider
	Tool       string
	Message    string
	Retryable  bool
	GrantURL   string
	Details    any
	Capability string
}

func (e *ExecutionError) Error() string {
	if strings.TrimSpace(e.Message) == "" {
		return string(e.Kind)
	}
	return e.Message
}

func MarshalResult(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func ResultIsError(content string) bool {
	var envelope ResultEnvelope
	if err := json.Unmarshal([]byte(content), &envelope); err != nil {
		return false
	}
	return !envelope.OK
}

func MarshalSuccess(toolName string, result any) (string, error) {
	return MarshalResult(ResultEnvelope{
		OK:     true,
		Tool:   strings.TrimSpace(toolName),
		Result: result,
	})
}

func MarshalError(toolName string, err error) (string, error) {
	envelope := ResultEnvelope{
		OK:   false,
		Tool: strings.TrimSpace(toolName),
	}

	var execErr *ExecutionError
	if errors.As(err, &execErr) {
		message := strings.TrimSpace(execErr.Message)
		if message == "" {
			message = string(execErr.Kind)
		}
		envelope.Error = message
		envelope.Meta = &ErrorEnvelope{
			Kind:       execErr.Kind,
			Provider:   execErr.Provider,
			Tool:       firstNonEmpty(execErr.Tool, toolName),
			Message:    message,
			Retryable:  execErr.Retryable,
			GrantURL:   strings.TrimSpace(execErr.GrantURL),
			Details:    execErr.Details,
			Capability: strings.TrimSpace(execErr.Capability),
		}
		return MarshalResult(envelope)
	}

	message := "tool execution failed"
	if err != nil && strings.TrimSpace(err.Error()) != "" {
		message = strings.TrimSpace(err.Error())
	}
	envelope.Error = message
	envelope.Meta = &ErrorEnvelope{
		Kind:    ErrorProvider,
		Tool:    strings.TrimSpace(toolName),
		Message: message,
	}
	return MarshalResult(envelope)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
