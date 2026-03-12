package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/url"
	"path/filepath"
	"strings"

	"goclaw/internal/domain"
)

type PolicyResolver interface {
	Resolve(ctx context.Context, profileID domain.ProfileID, roomID domain.RoomID) (domain.ToolPermissionPolicy, error)
}

type OperationKind string

const (
	OperationCommand   OperationKind = "command"
	OperationReadPath  OperationKind = "read_path"
	OperationWritePath OperationKind = "write_path"
	OperationNetwork   OperationKind = "network"
)

type CheckRequest struct {
	Kind    OperationKind
	Command string
	Path    string
	Host    string
}

type Decision struct {
	Allowed     bool                        `json:"allowed"`
	Reason      string                      `json:"reason"`
	MatchedRule string                      `json:"matched_rule,omitempty"`
	Kind        OperationKind               `json:"kind"`
	Request     CheckRequest                `json:"request"`
	Policy      domain.ToolPermissionPolicy `json:"-"`
}

type Authorizer struct {
	Policies PolicyResolver
	logger   *log.Logger
}

func NewAuthorizer(policies PolicyResolver, auditSink io.Writer) *Authorizer {
	var logger *log.Logger
	if auditSink != nil {
		logger = log.New(auditSink, "tool-permission ", 0)
	}
	return &Authorizer{
		Policies: policies,
		logger:   logger,
	}
}

func (a *Authorizer) Check(
	ctx context.Context,
	profileID domain.ProfileID,
	roomID domain.RoomID,
	request CheckRequest,
) (Decision, error) {
	policy, err := a.Policies.Resolve(ctx, profileID, roomID)
	if err != nil {
		return Decision{}, err
	}

	decision := evaluate(policy, request)
	if a.logger != nil {
		a.logger.Printf(
			"profile=%s room=%s kind=%s allowed=%t rule=%s reason=%s",
			profileID,
			roomID,
			decision.Kind,
			decision.Allowed,
			decision.MatchedRule,
			decision.Reason,
		)
	}
	return decision, nil
}

func evaluate(policy domain.ToolPermissionPolicy, request CheckRequest) Decision {
	switch request.Kind {
	case OperationCommand:
		return evaluateCommand(policy, request)
	case OperationReadPath, OperationWritePath:
		return evaluatePath(policy, request)
	case OperationNetwork:
		return evaluateNetwork(policy, request)
	default:
		return Decision{
			Allowed: false,
			Reason:  "unknown operation kind",
			Kind:    request.Kind,
			Request: request,
			Policy:  policy,
		}
	}
}

func DecisionForLocalToolCall(
	policy domain.ToolPermissionPolicy,
	call ToolCallBlock,
) (Decision, bool) {
	switch strings.TrimSpace(call.Name) {
	case "exec":
		var args struct {
			Program string `json:"program"`
			Workdir string `json:"workdir"`
		}
		if err := json.Unmarshal(call.Args, &args); err != nil {
			return invalidToolDecision(
				OperationCommand,
				CheckRequest{Kind: OperationCommand},
				fmt.Sprintf("parse exec args: %v", err),
				policy,
			), true
		}
		decision := evaluate(policy, CheckRequest{
			Kind:    OperationCommand,
			Command: args.Program,
		})
		if !decision.Allowed || strings.TrimSpace(args.Workdir) == "" {
			return decision, true
		}
		return evaluate(policy, CheckRequest{
			Kind: OperationWritePath,
			Path: args.Workdir,
		}), true
	case "read":
		var args struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(call.Args, &args); err != nil {
			return invalidToolDecision(
				OperationReadPath,
				CheckRequest{Kind: OperationReadPath},
				fmt.Sprintf("parse read args: %v", err),
				policy,
			), true
		}
		return evaluate(policy, CheckRequest{
			Kind: OperationReadPath,
			Path: args.Path,
		}), true
	case "write":
		var args struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(call.Args, &args); err != nil {
			return invalidToolDecision(
				OperationWritePath,
				CheckRequest{Kind: OperationWritePath},
				fmt.Sprintf("parse write args: %v", err),
				policy,
			), true
		}
		return evaluate(policy, CheckRequest{
			Kind: OperationWritePath,
			Path: args.Path,
		}), true
	case "fetch":
		var args struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal(call.Args, &args); err != nil {
			return invalidToolDecision(
				OperationNetwork,
				CheckRequest{Kind: OperationNetwork},
				fmt.Sprintf("parse fetch args: %v", err),
				policy,
			), true
		}
		parsedURL, err := url.Parse(strings.TrimSpace(args.URL))
		if err != nil {
			return invalidToolDecision(
				OperationNetwork,
				CheckRequest{Kind: OperationNetwork, Host: strings.TrimSpace(args.URL)},
				fmt.Sprintf("parse fetch url: %v", err),
				policy,
			), true
		}
		return evaluate(policy, CheckRequest{
			Kind: OperationNetwork,
			Host: parsedURL.Hostname(),
		}), true
	default:
		return Decision{}, false
	}
}

func evaluateCommand(policy domain.ToolPermissionPolicy, request CheckRequest) Decision {
	command := normalizeCommand(request.Command)
	decision := Decision{
		Allowed: false,
		Reason:  "command is not allowed by room policy",
		Kind:    request.Kind,
		Request: request,
		Policy:  policy,
	}
	if command == "" {
		decision.Reason = "missing command"
		return decision
	}
	if match, ok := matchExact(policy.DeniedCommands, command); ok {
		decision.Reason = "command matched deny rule"
		decision.MatchedRule = match
		return decision
	}

	switch policy.CommandsMode {
	case domain.ToolPermissionAllowAll:
		decision.Allowed = true
		decision.Reason = "command allowed by allow_all policy"
		return decision
	case domain.ToolPermissionAllowList:
		if match, ok := matchExact(policy.AllowedCommands, command); ok {
			decision.Allowed = true
			decision.Reason = "command matched allow rule"
			decision.MatchedRule = match
			return decision
		}
		decision.Reason = "command did not match any allow rule"
		return decision
	default:
		return decision
	}
}

func invalidToolDecision(
	kind OperationKind,
	request CheckRequest,
	reason string,
	policy domain.ToolPermissionPolicy,
) Decision {
	return Decision{
		Allowed: false,
		Reason:  reason,
		Kind:    kind,
		Request: request,
		Policy:  policy,
	}
}

func evaluatePath(policy domain.ToolPermissionPolicy, request CheckRequest) Decision {
	path := normalizePath(request.Path)
	decision := Decision{
		Allowed: false,
		Reason:  "path is not allowed by room policy",
		Kind:    request.Kind,
		Request: request,
		Policy:  policy,
	}
	if path == "" {
		decision.Reason = "missing path"
		return decision
	}
	if match, ok := matchPathPrefix(policy.DeniedPaths, path); ok {
		decision.Reason = "path matched deny rule"
		decision.MatchedRule = match
		return decision
	}

	switch policy.PathsMode {
	case domain.ToolPermissionAllowAll:
		decision.Allowed = true
		decision.Reason = "path allowed by allow_all policy"
		return decision
	case domain.ToolPermissionAllowList:
		if match, ok := matchPathPrefix(policy.AllowedPaths, path); ok {
			decision.Allowed = true
			decision.Reason = "path matched allow rule"
			decision.MatchedRule = match
			return decision
		}
		decision.Reason = "path did not match any allow rule"
		return decision
	default:
		return decision
	}
}

func evaluateNetwork(policy domain.ToolPermissionPolicy, request CheckRequest) Decision {
	host := normalizeHost(request.Host)
	decision := Decision{
		Allowed: false,
		Reason:  "network access is not allowed by room policy",
		Kind:    request.Kind,
		Request: request,
		Policy:  policy,
	}
	if host == "" {
		decision.Reason = "missing host"
		return decision
	}
	if match, ok := matchHost(policy.DeniedHosts, host); ok {
		decision.Reason = "host matched deny rule"
		decision.MatchedRule = match
		return decision
	}

	switch policy.NetworkMode {
	case domain.ToolPermissionAllowAll:
		decision.Allowed = true
		decision.Reason = "host allowed by allow_all policy"
		return decision
	case domain.ToolPermissionAllowList:
		if match, ok := matchHost(policy.AllowedHosts, host); ok {
			decision.Allowed = true
			decision.Reason = "host matched allow rule"
			decision.MatchedRule = match
			return decision
		}
		decision.Reason = "host did not match any allow rule"
		return decision
	default:
		return decision
	}
}

func normalizeCommand(value string) string {
	fields := strings.Fields(strings.TrimSpace(value))
	if len(fields) == 0 {
		return ""
	}
	return filepath.Base(fields[0])
}

func normalizePath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return filepath.Clean(value)
}

func normalizeHost(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimSuffix(value, ".")
	return value
}

func matchExact(candidates []string, value string) (string, bool) {
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate) == value {
			return candidate, true
		}
	}
	return "", false
}

func matchPathPrefix(prefixes []string, path string) (string, bool) {
	for _, prefix := range prefixes {
		normalized := normalizePath(prefix)
		if normalized == "" {
			continue
		}
		if path == normalized {
			return normalized, true
		}
		if normalized == string(filepath.Separator) {
			return normalized, true
		}
		if strings.HasPrefix(path, normalized+string(filepath.Separator)) {
			return normalized, true
		}
	}
	return "", false
}

func matchHost(hosts []string, host string) (string, bool) {
	for _, candidate := range hosts {
		normalized := normalizeHost(candidate)
		if normalized == "" {
			continue
		}
		if host == normalized || strings.HasSuffix(host, "."+normalized) {
			return normalized, true
		}
	}
	return "", false
}

func RenderDecision(decision Decision) string {
	return fmt.Sprintf(
		"kind=%s allowed=%t reason=%s matched_rule=%s",
		decision.Kind,
		decision.Allowed,
		decision.Reason,
		decision.MatchedRule,
	)
}
