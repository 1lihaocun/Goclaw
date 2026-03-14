package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

const mcpProtocolVersion = "2024-11-05"

type ToolClient interface {
	ListTools(context.Context, serverConfig) ([]listedTool, error)
	CallTool(context.Context, serverConfig, string, json.RawMessage) (map[string]any, error)
}

type Client struct {
	mu       sync.Mutex
	sessions map[string]*persistentSession
	closed   bool
}

type rpcEnvelope struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type stdioSession struct {
	cmd    *exec.Cmd
	cancel context.CancelFunc
	stdin  io.WriteCloser
	reader *bufio.Reader
	nextID int
	once   sync.Once
}

type persistentSession struct {
	server      serverConfig
	mu          sync.Mutex
	session     *stdioSession
	initialized bool
}

func (c *Client) ListTools(ctx context.Context, server serverConfig) ([]listedTool, error) {
	callCtx, cancel := context.WithTimeout(ctx, server.handshakeTimeout())
	defer cancel()

	session, err := c.sessionFor(server)
	if err != nil {
		return nil, err
	}
	result, err := session.requestJSON(callCtx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}

	var payload struct {
		Tools []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		return nil, fmt.Errorf("decode tools/list result: %w", err)
	}
	tools := make([]listedTool, 0, len(payload.Tools))
	for _, item := range payload.Tools {
		tools = append(tools, listedTool{
			Name:        strings.TrimSpace(item.Name),
			Description: strings.TrimSpace(item.Description),
			InputSchema: append(json.RawMessage(nil), item.InputSchema...),
		})
	}
	return tools, nil
}

func (c *Client) CallTool(
	ctx context.Context,
	server serverConfig,
	toolName string,
	args json.RawMessage,
) (map[string]any, error) {
	callCtx, cancel := context.WithTimeout(ctx, server.callTimeout())
	defer cancel()

	session, err := c.sessionFor(server)
	if err != nil {
		return nil, err
	}
	arguments, err := decodeArguments(args)
	if err != nil {
		return nil, err
	}
	result, err := session.requestJSON(callCtx, "tools/call", map[string]any{
		"name":      toolName,
		"arguments": arguments,
	})
	if err != nil {
		return nil, err
	}

	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		return nil, fmt.Errorf("decode tools/call result: %w", err)
	}
	return payload, nil
}

func (c *Client) Close() error {
	if c == nil {
		return nil
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	sessions := make([]*persistentSession, 0, len(c.sessions))
	for _, session := range c.sessions {
		sessions = append(sessions, session)
	}
	c.sessions = nil
	c.mu.Unlock()

	var closeErr error
	for _, session := range sessions {
		if session == nil {
			continue
		}
		if err := session.close(); err != nil {
			closeErr = errors.Join(closeErr, err)
		}
	}
	return closeErr
}

func (c *Client) sessionFor(server serverConfig) (*persistentSession, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil, fmt.Errorf("mcp client is closed")
	}
	if c.sessions == nil {
		c.sessions = make(map[string]*persistentSession)
	}
	key := strings.TrimSpace(server.Name)
	if existing, ok := c.sessions[key]; ok {
		return existing, nil
	}
	session := &persistentSession{server: server}
	c.sessions[key] = session
	return session, nil
}

func (s *persistentSession) requestJSON(
	ctx context.Context,
	method string,
	params any,
) (json.RawMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.requestWithRetryLocked(ctx, method, params)
}

func (s *persistentSession) requestWithRetryLocked(
	ctx context.Context,
	method string,
	params any,
) (json.RawMessage, error) {
	if err := s.ensureSessionLocked(ctx); err != nil {
		return nil, err
	}

	result, err := s.runRequestLocked(ctx, method, params)
	if err == nil {
		return result, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	_ = s.closeLocked()
	if err := s.ensureSessionLocked(ctx); err != nil {
		return nil, err
	}
	return s.runRequestLocked(ctx, method, params)
}

func (s *persistentSession) ensureSessionLocked(ctx context.Context) error {
	if s.session != nil && s.initialized {
		return nil
	}

	lowLevel, err := newSession(s.server)
	if err != nil {
		return err
	}
	s.session = lowLevel
	s.initialized = false

	if _, err := s.runRawRequestLocked(ctx, "initialize", map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "goclaw",
			"version": "0.0.1-dev",
		},
	}); err != nil {
		_ = s.closeLocked()
		return fmt.Errorf("initialize mcp server %q: %w", s.server.Name, err)
	}
	if err := s.runNotifyLocked(ctx, "notifications/initialized", map[string]any{}); err != nil {
		_ = s.closeLocked()
		return fmt.Errorf("notify initialized for %q: %w", s.server.Name, err)
	}

	s.initialized = true
	return nil
}

func (s *persistentSession) runRequestLocked(
	ctx context.Context,
	method string,
	params any,
) (json.RawMessage, error) {
	result, err := s.runRawRequestLocked(ctx, method, params)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *persistentSession) runRawRequestLocked(
	ctx context.Context,
	method string,
	params any,
) (json.RawMessage, error) {
	if s.session == nil {
		return nil, fmt.Errorf("mcp session is not initialized")
	}

	done := make(chan struct{})
	defer close(done)
	go func(session *stdioSession) {
		select {
		case <-ctx.Done():
			session.close()
		case <-done:
		}
	}(s.session)

	result, err := s.session.request(method, params)
	if err != nil {
		session := s.session
		s.initialized = false
		s.session = nil
		if session != nil {
			session.close()
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	return result, nil
}

func (s *persistentSession) runNotifyLocked(ctx context.Context, method string, params any) error {
	if s.session == nil {
		return fmt.Errorf("mcp session is not initialized")
	}

	done := make(chan struct{})
	defer close(done)
	go func(session *stdioSession) {
		select {
		case <-ctx.Done():
			session.close()
		case <-done:
		}
	}(s.session)

	if err := s.session.notify(method, params); err != nil {
		session := s.session
		s.initialized = false
		s.session = nil
		if session != nil {
			session.close()
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	return nil
}

func (s *persistentSession) close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeLocked()
}

func (s *persistentSession) closeLocked() error {
	if s.session == nil {
		s.initialized = false
		return nil
	}
	session := s.session
	s.session = nil
	s.initialized = false
	session.close()
	return nil
}

func newSession(server serverConfig) (*stdioSession, error) {
	commandCtx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(commandCtx, server.Command, server.Args...)
	cmd.Env = mergedEnv(server.Env)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("mcp server %q stdin pipe: %w", server.Name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		_ = stdin.Close()
		return nil, fmt.Errorf("mcp server %q stdout pipe: %w", server.Name, err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		cancel()
		_ = stdin.Close()
		return nil, fmt.Errorf("start mcp server %q: %w", server.Name, err)
	}
	return &stdioSession{
		cmd:    cmd,
		cancel: cancel,
		stdin:  stdin,
		reader: bufio.NewReader(stdout),
		nextID: 1,
	}, nil
}

func (s *stdioSession) close() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		if s.stdin != nil {
			_ = s.stdin.Close()
		}
		if s.cancel != nil {
			s.cancel()
		}
		if s.cmd != nil {
			_ = s.cmd.Wait()
		}
	})
}

func (s *stdioSession) request(method string, params any) (json.RawMessage, error) {
	id := s.nextID
	s.nextID++
	if err := writeMessage(s.stdin, map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}); err != nil {
		return nil, err
	}

	expectedID := []byte(strconv.Itoa(id))
	for {
		payload, err := readMessage(s.reader)
		if err != nil {
			return nil, err
		}
		var envelope rpcEnvelope
		if err := json.Unmarshal(payload, &envelope); err != nil {
			return nil, fmt.Errorf("decode mcp response: %w", err)
		}

		if strings.TrimSpace(envelope.Method) != "" {
			if len(envelope.ID) == 0 {
				continue
			}
			if err := writeServerMethodNotFound(s.stdin, envelope.ID, envelope.Method); err != nil {
				return nil, err
			}
			continue
		}
		if !bytes.Equal(bytes.TrimSpace(envelope.ID), expectedID) {
			continue
		}
		if envelope.Error != nil {
			return nil, fmt.Errorf("rpc error %d: %s", envelope.Error.Code, strings.TrimSpace(envelope.Error.Message))
		}
		return append(json.RawMessage(nil), envelope.Result...), nil
	}
}

func (s *stdioSession) notify(method string, params any) error {
	return writeMessage(s.stdin, map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
}

func writeServerMethodNotFound(w io.Writer, rawID json.RawMessage, method string) error {
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      json.RawMessage(rawID),
		"error": map[string]any{
			"code":    -32601,
			"message": fmt.Sprintf("unsupported server request %q", strings.TrimSpace(method)),
		},
	}
	return writeMessage(w, payload)
}

func writeMessage(w io.Writer, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal mcp message: %w", err)
	}
	if _, err := fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(data)); err != nil {
		return fmt.Errorf("write mcp header: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("write mcp body: %w", err)
	}
	return nil
}

func readMessage(r *bufio.Reader) (json.RawMessage, error) {
	contentLength := 0
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("read mcp header: %w", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(key), "Content-Length") {
			continue
		}
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return nil, fmt.Errorf("parse content length %q: %w", value, err)
		}
		contentLength = parsed
	}
	if contentLength <= 0 {
		return nil, fmt.Errorf("missing content length")
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, fmt.Errorf("read mcp body: %w", err)
	}
	return body, nil
}

func decodeArguments(raw json.RawMessage) (map[string]any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]any{}, nil
	}
	var arguments map[string]any
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, fmt.Errorf("decode mcp tool args: %w", err)
	}
	if arguments == nil {
		return map[string]any{}, nil
	}
	return arguments, nil
}

func mergedEnv(extra map[string]string) []string {
	env := append([]string(nil), os.Environ()...)
	for key, value := range extra {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		env = append(env, key+"="+value)
	}
	return env
}

func (s serverConfig) handshakeTimeout() time.Duration {
	if s.HandshakeTimeoutSec > 0 {
		return time.Duration(s.HandshakeTimeoutSec) * time.Second
	}
	return 10 * time.Second
}

func (s serverConfig) callTimeout() time.Duration {
	if s.CallTimeoutSec > 0 {
		return time.Duration(s.CallTimeoutSec) * time.Second
	}
	return 20 * time.Second
}
