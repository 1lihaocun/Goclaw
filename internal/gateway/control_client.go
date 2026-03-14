package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"goclaw/internal/config"
)

const defaultControlClientTimeout = 750 * time.Millisecond

type ControlClient struct {
	baseURL    string
	httpClient *http.Client
}

func NewControlClient(cfg config.Config) *ControlClient {
	if !cfg.Gateway.Control.Enabled {
		return nil
	}

	baseURL := strings.TrimRight(ControlURLFromConfig(cfg), "/")
	if baseURL == "" {
		return nil
	}

	return &ControlClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: defaultControlClientTimeout,
		},
	}
}

func (c *ControlClient) Status(ctx context.Context) (StatusSnapshot, bool, error) {
	var view controlStatusView
	used, err := c.get(ctx, "/api/control/status", &view)
	if err != nil || !used {
		return StatusSnapshot{}, used, err
	}
	return view.Snapshot, true, nil
}

func (c *ControlClient) InspectAccount(
	ctx context.Context,
	target AccountSelector,
) (AccountInspectView, bool, error) {
	query := make([]string, 0, 3)
	if strings.TrimSpace(string(target.Provider)) != "" {
		query = append(query, "provider="+encodeQueryComponent(string(target.Provider)))
	}
	if strings.TrimSpace(target.AccountID) != "" {
		query = append(query, "account_id="+encodeQueryComponent(target.AccountID))
	}
	if strings.TrimSpace(string(target.ProfileID)) != "" {
		query = append(query, "profile_id="+encodeQueryComponent(string(target.ProfileID)))
	}

	path := "/api/control/channels/inspect"
	if len(query) > 0 {
		path += "?" + strings.Join(query, "&")
	}

	var view AccountInspectView
	used, err := c.get(ctx, path, &view)
	if err != nil || !used {
		return AccountInspectView{}, used, err
	}
	return view, true, nil
}

func (c *ControlClient) StartAccount(
	ctx context.Context,
	target AccountSelector,
) (LifecycleActionResult, bool, error) {
	return c.lifecycleAction(ctx, "/api/control/channels/start", target)
}

func (c *ControlClient) StopAccount(
	ctx context.Context,
	target AccountSelector,
) (LifecycleActionResult, bool, error) {
	return c.lifecycleAction(ctx, "/api/control/channels/stop", target)
}

func (c *ControlClient) RestartAccount(
	ctx context.Context,
	target AccountSelector,
) (LifecycleActionResult, bool, error) {
	return c.lifecycleAction(ctx, "/api/control/channels/restart", target)
}

func (c *ControlClient) lifecycleAction(
	ctx context.Context,
	path string,
	target AccountSelector,
) (LifecycleActionResult, bool, error) {
	var result LifecycleActionResult
	used, err := c.post(ctx, path, target, &result)
	if err != nil || !used {
		return LifecycleActionResult{}, used, err
	}
	return result, true, nil
}

func (c *ControlClient) get(ctx context.Context, path string, target any) (bool, error) {
	return c.do(ctx, http.MethodGet, path, nil, target)
}

func (c *ControlClient) post(ctx context.Context, path string, body any, target any) (bool, error) {
	return c.do(ctx, http.MethodPost, path, body, target)
}

func (c *ControlClient) do(
	ctx context.Context,
	method string,
	path string,
	body any,
	target any,
) (bool, error) {
	if c == nil || strings.TrimSpace(c.baseURL) == "" || c.httpClient == nil {
		return false, nil
	}

	var requestBody io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return false, err
		}
		requestBody = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, requestBody)
	if err != nil {
		return false, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, nil
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	payload, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return true, err
	}

	if resp.StatusCode >= http.StatusBadRequest {
		return true, fmt.Errorf("control api %s %s returned %d: %s", method, path, resp.StatusCode, decodeControlAPIError(payload))
	}

	if err := json.Unmarshal(payload, target); err != nil {
		return true, fmt.Errorf("decode control api %s %s: %w", method, path, err)
	}
	return true, nil
}

func decodeControlAPIError(payload []byte) string {
	var view struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(payload, &view); err == nil && strings.TrimSpace(view.Error) != "" {
		return strings.TrimSpace(view.Error)
	}

	trimmed := strings.TrimSpace(string(payload))
	if trimmed == "" {
		return "unknown error"
	}
	return trimmed
}

func encodeQueryComponent(value string) string {
	return url.QueryEscape(strings.TrimSpace(value))
}

func ControlURLFromConfig(cfg config.Config) string {
	control := cfg.Gateway.Control
	if strings.TrimSpace(control.Host) == "" || control.Port <= 0 {
		return ""
	}
	return "http://" + net.JoinHostPort(displayControlHost(control.Host), strconv.Itoa(control.Port))
}
