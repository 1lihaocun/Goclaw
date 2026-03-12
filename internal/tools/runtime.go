package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"goclaw/internal/domain"
)

const (
	defaultCommandTimeout = 10 * time.Second
	defaultFetchTimeout   = 10 * time.Second
	defaultFetchMaxBytes  = 64 << 10
)

var ErrPermissionDenied = errors.New("tools: permission denied")

type PermissionDeniedError struct {
	Decision Decision
}

func (e PermissionDeniedError) Error() string {
	return fmt.Sprintf("%s: %s", ErrPermissionDenied, RenderDecision(e.Decision))
}

func (e PermissionDeniedError) Unwrap() error {
	return ErrPermissionDenied
}

type LocalRuntime struct {
	Authorizer *Authorizer
	HTTPClient *http.Client
}

type CommandRequest struct {
	Program string
	Args    []string
	Workdir string
	Stdin   string
	Timeout time.Duration
}

type CommandResult struct {
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

type ReadFileResult struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Bytes   int    `json:"bytes"`
}

type WriteFileRequest struct {
	Path       string
	Content    string
	Append     bool
	CreateDirs bool
}

type WriteFileResult struct {
	Path         string `json:"path"`
	BytesWritten int    `json:"bytes_written"`
	Append       bool   `json:"append"`
}

type FetchRequest struct {
	URL      string
	Timeout  time.Duration
	MaxBytes int64
}

type FetchResult struct {
	URL         string `json:"url"`
	StatusCode  int    `json:"status_code"`
	ContentType string `json:"content_type,omitempty"`
	Body        string `json:"body"`
	Bytes       int    `json:"bytes"`
}

func NewLocalRuntime(policies PolicyResolver, auditSink io.Writer) *LocalRuntime {
	return &LocalRuntime{
		Authorizer: NewAuthorizer(policies, auditSink),
		HTTPClient: &http.Client{Timeout: defaultFetchTimeout},
	}
}

func (r *LocalRuntime) RunCommand(
	ctx context.Context,
	profileID domain.ProfileID,
	roomID domain.RoomID,
	request CommandRequest,
) (CommandResult, error) {
	program := strings.TrimSpace(request.Program)
	if program == "" {
		return CommandResult{}, errors.New("tools: missing command program")
	}
	if r.Authorizer == nil {
		return CommandResult{}, errors.New("tools: authorizer is not configured")
	}

	decision, err := r.Authorizer.Check(ctx, profileID, roomID, CheckRequest{
		Kind:    OperationCommand,
		Command: program,
	})
	if err != nil {
		return CommandResult{}, err
	}
	if !decision.Allowed {
		return CommandResult{}, PermissionDeniedError{Decision: decision}
	}

	workdir := strings.TrimSpace(request.Workdir)
	if workdir != "" {
		pathDecision, err := r.Authorizer.Check(ctx, profileID, roomID, CheckRequest{
			Kind: OperationWritePath,
			Path: workdir,
		})
		if err != nil {
			return CommandResult{}, err
		}
		if !pathDecision.Allowed {
			return CommandResult{}, PermissionDeniedError{Decision: pathDecision}
		}
	}

	timeout := request.Timeout
	if timeout <= 0 {
		timeout = defaultCommandTimeout
	}
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(commandCtx, program, request.Args...)
	if workdir != "" {
		cmd.Dir = workdir
	}
	if request.Stdin != "" {
		cmd.Stdin = strings.NewReader(request.Stdin)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	result := CommandResult{
		ExitCode: 0,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
	}
	if err == nil {
		return result, nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	}
	if errors.Is(commandCtx.Err(), context.DeadlineExceeded) {
		return result, fmt.Errorf("tools: command timed out after %s", timeout)
	}
	return result, err
}

func (r *LocalRuntime) ReadFile(
	ctx context.Context,
	profileID domain.ProfileID,
	roomID domain.RoomID,
	path string,
) (ReadFileResult, error) {
	normalized := normalizePath(path)
	if normalized == "" {
		return ReadFileResult{}, errors.New("tools: missing file path")
	}
	if r.Authorizer == nil {
		return ReadFileResult{}, errors.New("tools: authorizer is not configured")
	}
	decision, err := r.Authorizer.Check(ctx, profileID, roomID, CheckRequest{
		Kind: OperationReadPath,
		Path: normalized,
	})
	if err != nil {
		return ReadFileResult{}, err
	}
	if !decision.Allowed {
		return ReadFileResult{}, PermissionDeniedError{Decision: decision}
	}

	data, err := os.ReadFile(normalized)
	if err != nil {
		return ReadFileResult{}, err
	}
	return ReadFileResult{
		Path:    normalized,
		Content: string(data),
		Bytes:   len(data),
	}, nil
}

func (r *LocalRuntime) WriteFile(
	ctx context.Context,
	profileID domain.ProfileID,
	roomID domain.RoomID,
	request WriteFileRequest,
) (WriteFileResult, error) {
	normalized := normalizePath(request.Path)
	if normalized == "" {
		return WriteFileResult{}, errors.New("tools: missing file path")
	}
	if r.Authorizer == nil {
		return WriteFileResult{}, errors.New("tools: authorizer is not configured")
	}
	decision, err := r.Authorizer.Check(ctx, profileID, roomID, CheckRequest{
		Kind: OperationWritePath,
		Path: normalized,
	})
	if err != nil {
		return WriteFileResult{}, err
	}
	if !decision.Allowed {
		return WriteFileResult{}, PermissionDeniedError{Decision: decision}
	}

	if request.CreateDirs {
		if err := os.MkdirAll(filepath.Dir(normalized), 0o755); err != nil {
			return WriteFileResult{}, err
		}
	}

	flag := os.O_CREATE | os.O_WRONLY
	if request.Append {
		flag |= os.O_APPEND
	} else {
		flag |= os.O_TRUNC
	}

	file, err := os.OpenFile(normalized, flag, 0o644)
	if err != nil {
		return WriteFileResult{}, err
	}
	defer file.Close()

	n, err := file.WriteString(request.Content)
	if err != nil {
		return WriteFileResult{}, err
	}
	return WriteFileResult{
		Path:         normalized,
		BytesWritten: n,
		Append:       request.Append,
	}, nil
}

func (r *LocalRuntime) FetchURL(
	ctx context.Context,
	profileID domain.ProfileID,
	roomID domain.RoomID,
	request FetchRequest,
) (FetchResult, error) {
	if r.Authorizer == nil {
		return FetchResult{}, errors.New("tools: authorizer is not configured")
	}

	parsedURL, err := url.Parse(strings.TrimSpace(request.URL))
	if err != nil {
		return FetchResult{}, fmt.Errorf("tools: parse url: %w", err)
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return FetchResult{}, fmt.Errorf("tools: unsupported url scheme %q", parsedURL.Scheme)
	}

	host := normalizeHost(parsedURL.Hostname())
	decision, err := r.Authorizer.Check(ctx, profileID, roomID, CheckRequest{
		Kind: OperationNetwork,
		Host: host,
	})
	if err != nil {
		return FetchResult{}, err
	}
	if !decision.Allowed {
		return FetchResult{}, PermissionDeniedError{Decision: decision}
	}

	timeout := request.Timeout
	if timeout <= 0 {
		timeout = defaultFetchTimeout
	}
	maxBytes := request.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultFetchMaxBytes
	}

	httpClient := r.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: timeout}
	}

	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	httpRequest, err := http.NewRequestWithContext(requestCtx, http.MethodGet, parsedURL.String(), nil)
	if err != nil {
		return FetchResult{}, err
	}
	response, err := httpClient.Do(httpRequest)
	if err != nil {
		return FetchResult{}, err
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, maxBytes))
	if err != nil {
		return FetchResult{}, err
	}
	return FetchResult{
		URL:         parsedURL.String(),
		StatusCode:  response.StatusCode,
		ContentType: strings.TrimSpace(response.Header.Get("Content-Type")),
		Body:        string(body),
		Bytes:       len(body),
	}, nil
}
