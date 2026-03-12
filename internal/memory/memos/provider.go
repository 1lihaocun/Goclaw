package memos

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"goclaw/internal/memory"
)

const defaultBaseURL = "https://memos.memtensor.cn/api/openmem/v1"

type Config struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
}

type Provider struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

func New(cfg Config) *Provider {
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}

	return &Provider{
		baseURL:    normalizeBaseURL(cfg.BaseURL),
		apiKey:     strings.TrimSpace(cfg.APIKey),
		httpClient: httpClient,
	}
}

func (p *Provider) Name() string {
	return "memos"
}

func (p *Provider) Search(
	ctx context.Context,
	scope memory.Scope,
	query memory.SearchQuery,
) ([]memory.SearchResult, error) {
	if !p.configured() || strings.TrimSpace(query.Text) == "" {
		return nil, nil
	}

	limit := query.Limit
	if limit <= 0 {
		limit = 3
	}

	payload := map[string]any{
		"query":                   strings.TrimSpace(query.Text),
		"user_id":                 resolveUserID(scope),
		"conversation_id":         resolveConversationID(scope),
		"memory_limit_number":     limit,
		"include_preference":      true,
		"preference_limit_number": limit,
		"include_tool_memory":     false,
		"include_skill":           false,
	}

	var response struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			MemoryDetailList []struct {
				ID             string   `json:"id"`
				MemoryKey      string   `json:"memory_key"`
				MemoryValue    string   `json:"memory_value"`
				MemoryType     string   `json:"memory_type"`
				ConversationID string   `json:"conversation_id"`
				Tags           []string `json:"tags"`
				Confidence     float64  `json:"confidence"`
				Relativity     float64  `json:"relativity"`
			} `json:"memory_detail_list"`
			PreferenceDetailList []struct {
				ID             string  `json:"id"`
				PreferenceType string  `json:"preference_type"`
				Preference     string  `json:"preference"`
				Reasoning      string  `json:"reasoning"`
				ConversationID string  `json:"conversation_id"`
				Relativity     float64 `json:"relativity"`
			} `json:"preference_detail_list"`
			ToolMemoryDetailList []struct {
				ID             string  `json:"id"`
				ToolType       string  `json:"tool_type"`
				ToolValue      string  `json:"tool_value"`
				Experience     string  `json:"experience"`
				ConversationID string  `json:"conversation_id"`
				Relativity     float64 `json:"relativity"`
			} `json:"tool_memory_detail_list"`
		} `json:"data"`
	}
	if err := p.postJSON(ctx, "/search/memory", payload, &response); err != nil {
		return nil, err
	}
	if response.Code != 0 {
		return nil, fmt.Errorf("memos search failed: %s", strings.TrimSpace(response.Message))
	}

	results := make([]memory.SearchResult, 0, len(response.Data.MemoryDetailList)+len(response.Data.PreferenceDetailList))
	for _, item := range response.Data.MemoryDetailList {
		content := strings.TrimSpace(item.MemoryValue)
		if content == "" {
			continue
		}
		results = append(results, memory.SearchResult{
			ID:      item.ID,
			Content: content,
			Source:  "memory",
			Score:   selectScore(item.Relativity, item.Confidence),
			Metadata: map[string]string{
				"conversation_id": item.ConversationID,
				"memory_key":      strings.TrimSpace(item.MemoryKey),
				"memory_type":     strings.TrimSpace(item.MemoryType),
				"scope_kind":      string(scope.Kind),
				"tags":            strings.Join(item.Tags, ","),
			},
		})
	}
	for _, item := range response.Data.PreferenceDetailList {
		content := strings.TrimSpace(item.Preference)
		if content == "" {
			continue
		}
		results = append(results, memory.SearchResult{
			ID:      item.ID,
			Content: content,
			Source:  "preference",
			Score:   selectScore(item.Relativity, 0),
			Metadata: map[string]string{
				"conversation_id": item.ConversationID,
				"preference_type": strings.TrimSpace(item.PreferenceType),
				"reasoning":       strings.TrimSpace(item.Reasoning),
				"scope_kind":      string(scope.Kind),
			},
		})
	}
	for _, item := range response.Data.ToolMemoryDetailList {
		content := strings.TrimSpace(item.ToolValue)
		if content == "" {
			content = strings.TrimSpace(item.Experience)
		}
		if content == "" {
			continue
		}
		results = append(results, memory.SearchResult{
			ID:      item.ID,
			Content: content,
			Source:  "tool",
			Score:   selectScore(item.Relativity, 0),
			Metadata: map[string]string{
				"conversation_id": item.ConversationID,
				"tool_type":       strings.TrimSpace(item.ToolType),
				"scope_kind":      string(scope.Kind),
			},
		})
	}

	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return results[i].ID < results[j].ID
		}
		return results[i].Score > results[j].Score
	})
	if len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

func (p *Provider) Write(
	ctx context.Context,
	scope memory.Scope,
	records []memory.WriteRecord,
) error {
	if !p.configured() || len(records) == 0 {
		return nil
	}

	messages := make([]map[string]any, 0, len(records))
	for _, record := range records {
		content := strings.TrimSpace(record.Content)
		if content == "" {
			continue
		}

		message := map[string]any{
			"role":    normalizeRole(record.Metadata["role"]),
			"content": content,
		}
		if chatTime := resolveChatTime(record); chatTime != "" {
			message["chat_time"] = chatTime
		}
		messages = append(messages, message)
	}
	if len(messages) == 0 {
		return nil
	}

	payload := map[string]any{
		"user_id":         resolveUserID(scope),
		"conversation_id": resolveConversationID(scope),
		"messages":        messages,
		"app_id":          string(scope.ProfileID),
		"info": map[string]string{
			"profile_id": string(scope.ProfileID),
			"room_id":    string(scope.RoomID),
			"person_id":  string(scope.PersonID),
			"scope_kind": string(scope.Kind),
		},
		"async_mode": true,
	}

	var response struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := p.postJSON(ctx, "/add/message", payload, &response); err != nil {
		return err
	}
	if response.Code != 0 {
		return fmt.Errorf("memos write failed: %s", strings.TrimSpace(response.Message))
	}
	return nil
}

func (p *Provider) Health(_ context.Context) memory.HealthStatus {
	if !p.configured() {
		return memory.HealthStatus{
			Available: false,
			Detail:    "memos is not configured",
		}
	}
	return memory.HealthStatus{
		Available: true,
		Detail:    "memos configured",
	}
}

func (p *Provider) configured() bool {
	return p != nil &&
		strings.TrimSpace(p.baseURL) != "" &&
		strings.TrimSpace(p.apiKey) != ""
}

func (p *Provider) postJSON(ctx context.Context, path string, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		p.baseURL+path,
		bytes.NewReader(body),
	)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Token "+p.apiKey)
	req.Header.Set("Content-Type", "application/json")

	res, err := p.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	responseBody, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("memos request failed: status=%d body=%s", res.StatusCode, string(responseBody))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(responseBody, out)
}

func normalizeBaseURL(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return defaultBaseURL
	}
	return strings.TrimRight(trimmed, "/")
}

func resolveUserID(scope memory.Scope) string {
	switch scope.Kind {
	case memory.ScopeConversation:
		return fmt.Sprintf("conversation:%s:%s:%s", scope.ProfileID, scope.RoomID, scope.ConversationID)
	case memory.ScopeRoom:
		return fmt.Sprintf("room:%s:%s", scope.ProfileID, scope.RoomID)
	case memory.ScopePersonSummary:
		return fmt.Sprintf("person-summary:%s:%s", scope.ProfileID, scope.PersonID)
	case memory.ScopePersonPrivate:
		return fmt.Sprintf("person-private:%s:%s", scope.ProfileID, scope.PersonID)
	default:
		return fmt.Sprintf("scope:%s:%s:%s:%s", scope.Kind, scope.ProfileID, scope.RoomID, scope.PersonID)
	}
}

func resolveConversationID(scope memory.Scope) string {
	if scope.Kind == memory.ScopeConversation && strings.TrimSpace(string(scope.ConversationID)) != "" {
		return string(scope.ConversationID)
	}
	if strings.TrimSpace(string(scope.RoomID)) != "" {
		return string(scope.RoomID)
	}
	switch scope.Kind {
	case memory.ScopePersonSummary:
		return fmt.Sprintf("summary:%s:%s", scope.ProfileID, scope.PersonID)
	case memory.ScopePersonPrivate:
		return fmt.Sprintf("private:%s:%s", scope.ProfileID, scope.PersonID)
	default:
		return fmt.Sprintf("scope:%s:%s", scope.Kind, scope.ProfileID)
	}
}

func normalizeRole(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "assistant":
		return "assistant"
	case "system":
		return "system"
	case "tool":
		return "tool"
	default:
		return "user"
	}
}

func resolveChatTime(record memory.WriteRecord) string {
	if value := strings.TrimSpace(record.Metadata["chat_time"]); value != "" {
		return value
	}
	if record.CreatedAt.IsZero() {
		return ""
	}
	return record.CreatedAt.UTC().Format(time.RFC3339Nano)
}

func selectScore(primary, fallback float64) float64 {
	if primary > 0 {
		return primary
	}
	return fallback
}
