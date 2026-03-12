package feishu

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

const permissionDeniedCode = 99991672

var feishuGrantURLPattern = regexp.MustCompile(`https://[^\s,]+/app/[^\s,]+`)

type RequestError struct {
	Method     string
	Path       string
	StatusCode int
	Body       []byte
}

func (e *RequestError) Error() string {
	return fmt.Sprintf(
		"feishu request failed: method=%s path=%s status=%d body=%s",
		e.Method,
		e.Path,
		e.StatusCode,
		string(e.Body),
	)
}

type APIError struct {
	Operation string
	Code      int
	Message   string
	Body      []byte
}

func (e *APIError) Error() string {
	message := strings.TrimSpace(e.Message)
	if message == "" {
		message = "unknown error"
	}
	if strings.TrimSpace(e.Operation) == "" {
		return fmt.Sprintf("feishu api failed: %s", message)
	}
	return fmt.Sprintf("%s: %s", strings.TrimSpace(e.Operation), message)
}

type PermissionError struct {
	Code          int      `json:"code"`
	Message       string   `json:"message"`
	GrantURL      string   `json:"grant_url,omitempty"`
	MissingScopes []string `json:"missing_scopes,omitempty"`
}

func ExtractPermissionError(err error) (*PermissionError, bool) {
	if err == nil {
		return nil, false
	}

	var (
		requestErr *RequestError
		apiErr     *APIError
		body       []byte
	)
	switch {
	case errors.As(err, &apiErr):
		body = apiErr.Body
	case errors.As(err, &requestErr):
		body = requestErr.Body
	default:
		return nil, false
	}

	var payload struct {
		Code  int    `json:"code"`
		Msg   string `json:"msg"`
		Error struct {
			PermissionViolations []struct {
				URI string `json:"uri"`
			} `json:"permission_violations"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return nil, false
	}
	if payload.Code != permissionDeniedCode {
		return nil, false
	}

	missingScopes := make([]string, 0, len(payload.Error.PermissionViolations))
	for _, violation := range payload.Error.PermissionViolations {
		scope := scopeNameFromGrantURL(violation.URI)
		if scope == "" {
			continue
		}
		missingScopes = append(missingScopes, correctScopeName(scope))
	}

	grantURL := ""
	if matched := feishuGrantURLPattern.FindString(payload.Msg); matched != "" {
		grantURL = correctGrantURL(matched)
	}
	if grantURL == "" {
		for _, violation := range payload.Error.PermissionViolations {
			if corrected := correctGrantURL(strings.TrimSpace(violation.URI)); corrected != "" {
				grantURL = corrected
				break
			}
		}
	}

	return &PermissionError{
		Code:          payload.Code,
		Message:       strings.TrimSpace(payload.Msg),
		GrantURL:      grantURL,
		MissingScopes: uniqueStrings(missingScopes),
	}, true
}

func newAPIError(operation string, code int, message string, body []byte) error {
	return &APIError{
		Operation: strings.TrimSpace(operation),
		Code:      code,
		Message:   strings.TrimSpace(message),
		Body:      cloneBytes(body),
	}
}

func correctGrantURL(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	corrected := trimmed
	for wrong, right := range feishuScopeCorrections {
		corrected = strings.ReplaceAll(corrected, wrong, right)
		corrected = strings.ReplaceAll(corrected, url.QueryEscape(wrong), url.QueryEscape(right))
	}
	return corrected
}

func scopeNameFromGrantURL(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return ""
	}
	candidates := []string{
		parsed.Query().Get("scope"),
		parsed.Query().Get("scope_name"),
		parsed.Query().Get("permission"),
	}
	for _, candidate := range candidates {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

var feishuScopeCorrections = map[string]string{
	"contact:contact.base:readonly": "contact:user.base:readonly",
}

func correctScopeName(value string) string {
	trimmed := strings.TrimSpace(value)
	if corrected, ok := feishuScopeCorrections[trimmed]; ok {
		return corrected
	}
	return trimmed
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func cloneBytes(value []byte) []byte {
	if len(value) == 0 {
		return nil
	}
	out := make([]byte, len(value))
	copy(out, value)
	return out
}
