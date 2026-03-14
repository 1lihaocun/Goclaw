package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"goclaw/internal/config"
	"goclaw/internal/domain"
	"goclaw/internal/gateway"
	"goclaw/internal/runtime"
	sqlitestore "goclaw/internal/store/sqlite"
	toolperm "goclaw/internal/tools"
)

const version = "0.0.1-dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	configPath, args, err := parseGlobalArgs(args)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "parse args: %v\n", err)
		return 1
	}

	if len(args) > 0 && args[0] == "version" {
		_, _ = fmt.Fprintln(stdout, version)
		return 0
	}

	cfg, err := config.Load(config.LoadOptions{Path: configPath})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "load config: %v\n", err)
		return 1
	}
	app := runtime.New(cfg)

	if err := app.Initialize(ctx); err != nil {
		_, _ = fmt.Fprintf(stderr, "initialize goclaw: %v\n", err)
		return 1
	}
	defer func() {
		_ = app.Close()
	}()

	if len(args) > 0 {
		switch args[0] {
		case "serve":
			if err := gateway.NewServer(app).Serve(ctx); err != nil {
				_, _ = fmt.Fprintf(stderr, "serve goclaw: %v\n", err)
				return 1
			}
			return 0
		case "consent":
			if err := runConsentCommand(ctx, app, args[1:], stdout); err != nil {
				_, _ = fmt.Fprintf(stderr, "consent: %v\n", err)
				return 1
			}
			return 0
		case "conversations":
			if err := runConversationsCommand(ctx, app, args[1:], stdout); err != nil {
				_, _ = fmt.Fprintf(stderr, "conversations: %v\n", err)
				return 1
			}
			return 0
		case "memory":
			if err := runMemoryCommand(ctx, app, args[1:], stdout); err != nil {
				_, _ = fmt.Fprintf(stderr, "memory: %v\n", err)
				return 1
			}
			return 0
		case "permissions":
			if err := runPermissionsCommand(ctx, app, args[1:], stdout); err != nil {
				_, _ = fmt.Fprintf(stderr, "permissions: %v\n", err)
				return 1
			}
			return 0
		case "tools":
			if err := runToolsCommand(ctx, app, args[1:], stdout); err != nil {
				_, _ = fmt.Fprintf(stderr, "tools: %v\n", err)
				return 1
			}
			return 0
		case "mcp":
			if err := runMCPCommand(ctx, app, args[1:], stdout); err != nil {
				_, _ = fmt.Fprintf(stderr, "mcp: %v\n", err)
				return 1
			}
			return 0
		case "skills":
			if err := runSkillsCommand(ctx, app, args[1:], stdout); err != nil {
				_, _ = fmt.Fprintf(stderr, "skills: %v\n", err)
				return 1
			}
			return 0
		case "tooling":
			if err := runToolingCommand(ctx, app, args[1:], stdout); err != nil {
				_, _ = fmt.Fprintf(stderr, "tooling: %v\n", err)
				return 1
			}
			return 0
		case "feishu":
			if err := runFeishuCommand(ctx, app, args[1:], stdout); err != nil {
				_, _ = fmt.Fprintf(stderr, "feishu: %v\n", err)
				return 1
			}
			return 0
		case "gateway":
			if err := runGatewayCommand(ctx, app, args[1:], stdout); err != nil {
				_, _ = fmt.Fprintf(stderr, "gateway: %v\n", err)
				return 1
			}
			return 0
		}
	}

	_, _ = fmt.Fprintln(stdout, app.Summary(version))
	return 0
}

func parseGlobalArgs(args []string) (string, []string, error) {
	fs := flag.NewFlagSet("goclaw", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var configPath string
	fs.StringVar(&configPath, "config", "", "path to config file")

	if err := fs.Parse(args); err != nil {
		return "", nil, err
	}
	return strings.TrimSpace(configPath), fs.Args(), nil
}

func runConsentCommand(ctx context.Context, app *runtime.App, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: goclaw consent <get|set> [flags]")
	}

	switch args[0] {
	case "get":
		return runConsentGet(ctx, app, args[1:], stdout)
	case "set":
		return runConsentSet(ctx, app, args[1:], stdout)
	default:
		return fmt.Errorf("unknown consent subcommand %q", args[0])
	}
}

func runConsentGet(ctx context.Context, app *runtime.App, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("consent get", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var profileID string
	var roomID string
	var personID string
	fs.StringVar(&profileID, "profile-id", "", "profile id")
	fs.StringVar(&roomID, "room-id", "", "room id")
	fs.StringVar(&personID, "person-id", "", "person id")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := requireConsentTarget(profileID, roomID, personID); err != nil {
		return err
	}
	if err := ensureConsentTargetExists(ctx, app, profileID, roomID, personID); err != nil {
		return err
	}

	policy, err := app.Repos.ConsentPolicies.Resolve(
		ctx,
		domain.ProfileID(profileID),
		domain.RoomID(roomID),
		domain.PersonID(personID),
	)
	if err != nil {
		return err
	}

	room, err := app.Repos.Rooms.Get(ctx, domain.RoomID(roomID))
	if err != nil {
		return err
	}

	return writeConsentPolicy(stdout, room.Kind, policy)
}

func runConsentSet(ctx context.Context, app *runtime.App, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("consent set", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var profileID string
	var roomID string
	var personID string
	var accessLevel string
	var updatedBy string
	var expiresAt string
	var clearExpires bool
	fs.StringVar(&profileID, "profile-id", "", "profile id")
	fs.StringVar(&roomID, "room-id", "", "room id")
	fs.StringVar(&personID, "person-id", "", "person id")
	fs.StringVar(&accessLevel, "access-level", "", "consent access level")
	fs.StringVar(&updatedBy, "updated-by", defaultConsentUpdater(), "audit actor")
	fs.StringVar(&expiresAt, "expires-at", "", "optional RFC3339 expiry")
	fs.BoolVar(&clearExpires, "clear-expires", false, "clear the current expiry")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := requireConsentTarget(profileID, roomID, personID); err != nil {
		return err
	}
	if strings.TrimSpace(accessLevel) == "" {
		return errors.New("missing required flag --access-level")
	}
	if clearExpires && strings.TrimSpace(expiresAt) != "" {
		return errors.New("--expires-at and --clear-expires cannot be used together")
	}

	parsedAccessLevel, err := parseConsentAccessLevel(accessLevel)
	if err != nil {
		return err
	}
	if err := ensureConsentTargetExists(ctx, app, profileID, roomID, personID); err != nil {
		return err
	}

	expiresAtValue, err := resolveConsentExpiry(
		ctx,
		app,
		domain.ProfileID(profileID),
		domain.RoomID(roomID),
		domain.PersonID(personID),
		expiresAt,
		clearExpires,
	)
	if err != nil {
		return err
	}

	if err := app.Repos.ConsentPolicies.Upsert(ctx, domain.ConsentPolicy{
		ProfileID:   domain.ProfileID(profileID),
		RoomID:      domain.RoomID(roomID),
		PersonID:    domain.PersonID(personID),
		AccessLevel: parsedAccessLevel,
		ExpiresAt:   expiresAtValue,
		UpdatedBy:   updatedBy,
	}); err != nil {
		return err
	}

	policy, err := app.Repos.ConsentPolicies.Get(
		ctx,
		domain.ProfileID(profileID),
		domain.RoomID(roomID),
		domain.PersonID(personID),
	)
	if err != nil {
		return err
	}

	room, err := app.Repos.Rooms.Get(ctx, domain.RoomID(roomID))
	if err != nil {
		return err
	}

	return writeConsentPolicy(stdout, room.Kind, policy)
}

func requireConsentTarget(profileID, roomID, personID string) error {
	if strings.TrimSpace(profileID) == "" {
		return errors.New("missing required flag --profile-id")
	}
	if strings.TrimSpace(roomID) == "" {
		return errors.New("missing required flag --room-id")
	}
	if strings.TrimSpace(personID) == "" {
		return errors.New("missing required flag --person-id")
	}
	return nil
}

func parseConsentAccessLevel(value string) (domain.ConsentAccessLevel, error) {
	switch domain.ConsentAccessLevel(strings.TrimSpace(value)) {
	case domain.ConsentDeny, domain.ConsentSummary, domain.ConsentFull:
		return domain.ConsentAccessLevel(strings.TrimSpace(value)), nil
	default:
		return "", fmt.Errorf("invalid --access-level %q (expected deny, summary, or full)", value)
	}
}

func ensureConsentTargetExists(
	ctx context.Context,
	app *runtime.App,
	profileID string,
	roomID string,
	personID string,
) error {
	if _, err := app.Repos.Profiles.Get(ctx, domain.ProfileID(profileID)); err != nil {
		return err
	}
	if _, err := app.Repos.Rooms.Get(ctx, domain.RoomID(roomID)); err != nil {
		return err
	}
	if _, err := app.Repos.Persons.Get(ctx, domain.PersonID(personID)); err != nil {
		return err
	}
	return nil
}

func resolveConsentExpiry(
	ctx context.Context,
	app *runtime.App,
	profileID domain.ProfileID,
	roomID domain.RoomID,
	personID domain.PersonID,
	expiresAt string,
	clearExpires bool,
) (*time.Time, error) {
	if clearExpires {
		return nil, nil
	}
	if strings.TrimSpace(expiresAt) != "" {
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(expiresAt))
		if err != nil {
			return nil, fmt.Errorf("parse --expires-at: %w", err)
		}
		parsed = parsed.UTC()
		return &parsed, nil
	}

	existing, err := app.Repos.ConsentPolicies.Get(ctx, profileID, roomID, personID)
	if err == nil {
		return existing.ExpiresAt, nil
	}
	if !errors.Is(err, sqlitestore.ErrNotFound) {
		return nil, err
	}
	return nil, nil
}

func defaultConsentUpdater() string {
	user := strings.TrimSpace(os.Getenv("USER"))
	if user == "" {
		return "goclaw-cli"
	}
	return "cli:" + user
}

func writeConsentPolicy(stdout io.Writer, roomKind domain.RoomKind, policy domain.ConsentPolicy) error {
	type consentPolicyView struct {
		ProfileID   string `json:"profile_id"`
		RoomID      string `json:"room_id"`
		PersonID    string `json:"person_id"`
		RoomKind    string `json:"room_kind"`
		AccessLevel string `json:"access_level"`
		Explicit    bool   `json:"explicit"`
		UpdatedBy   string `json:"updated_by,omitempty"`
		ExpiresAt   string `json:"expires_at,omitempty"`
		CreatedAt   string `json:"created_at,omitempty"`
		UpdatedAt   string `json:"updated_at,omitempty"`
	}

	view := consentPolicyView{
		ProfileID:   string(policy.ProfileID),
		RoomID:      string(policy.RoomID),
		PersonID:    string(policy.PersonID),
		RoomKind:    string(roomKind),
		AccessLevel: string(policy.AccessLevel),
		Explicit:    policy.Explicit,
		UpdatedBy:   policy.UpdatedBy,
	}
	if policy.ExpiresAt != nil {
		view.ExpiresAt = policy.ExpiresAt.UTC().Format(time.RFC3339)
	}
	if !policy.CreatedAt.IsZero() {
		view.CreatedAt = policy.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	if !policy.UpdatedAt.IsZero() {
		view.UpdatedAt = policy.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}

	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(view)
}

func runPermissionsCommand(ctx context.Context, app *runtime.App, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: goclaw permissions <get|set|check> [flags]")
	}

	switch args[0] {
	case "get":
		return runPermissionsGet(ctx, app, args[1:], stdout)
	case "set":
		return runPermissionsSet(ctx, app, args[1:], stdout)
	case "check":
		return runPermissionsCheck(ctx, app, args[1:], stdout)
	default:
		return fmt.Errorf("unknown permissions subcommand %q", args[0])
	}
}

func runPermissionsGet(ctx context.Context, app *runtime.App, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("permissions get", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var profileID string
	var roomID string
	fs.StringVar(&profileID, "profile-id", "", "profile id")
	fs.StringVar(&roomID, "room-id", "", "room id")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := requirePermissionTarget(profileID, roomID); err != nil {
		return err
	}
	if err := ensurePermissionTargetExists(ctx, app, profileID, roomID); err != nil {
		return err
	}

	policy, err := app.Repos.ToolPermissions.Resolve(ctx, domain.ProfileID(profileID), domain.RoomID(roomID))
	if err != nil {
		return err
	}
	return writeToolPermissionPolicy(stdout, policy)
}

func runPermissionsSet(ctx context.Context, app *runtime.App, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("permissions set", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var profileID string
	var roomID string
	var updatedBy string
	var commandsMode optionalStringFlag
	var pathsMode optionalStringFlag
	var networkMode optionalStringFlag
	var channelIntrospectionMode optionalStringFlag
	var channelReadMode optionalStringFlag
	var channelWriteMode optionalStringFlag
	var channelSensitiveMode optionalStringFlag
	var mcpIntrospectionMode optionalStringFlag
	var mcpReadMode optionalStringFlag
	var mcpWriteMode optionalStringFlag
	var mcpSensitiveMode optionalStringFlag
	var allowCommands optionalCSVFlag
	var denyCommands optionalCSVFlag
	var allowPaths optionalCSVFlag
	var denyPaths optionalCSVFlag
	var allowHosts optionalCSVFlag
	var denyHosts optionalCSVFlag
	var allowChannelTools optionalCSVFlag
	var denyChannelTools optionalCSVFlag
	var allowChannelProviders optionalCSVFlag
	var denyChannelProviders optionalCSVFlag
	var allowMCPTools optionalCSVFlag
	var denyMCPTools optionalCSVFlag
	var allowMCPServers optionalCSVFlag
	var denyMCPServers optionalCSVFlag

	fs.StringVar(&profileID, "profile-id", "", "profile id")
	fs.StringVar(&roomID, "room-id", "", "room id")
	fs.StringVar(&updatedBy, "updated-by", defaultPermissionUpdater(), "audit actor")
	fs.Var(&commandsMode, "commands-mode", "command mode: deny_all, allow_all, allow_list")
	fs.Var(&pathsMode, "paths-mode", "path mode: deny_all, allow_all, allow_list")
	fs.Var(&networkMode, "network-mode", "network mode: deny_all, allow_all, allow_list")
	fs.Var(&channelIntrospectionMode, "channel-introspection-mode", "channel introspection mode: deny_all, allow_all, allow_list")
	fs.Var(&channelReadMode, "channel-read-mode", "channel read mode: deny_all, allow_all, allow_list")
	fs.Var(&channelWriteMode, "channel-write-mode", "channel write mode: deny_all, allow_all, allow_list")
	fs.Var(&channelSensitiveMode, "channel-sensitive-mode", "channel sensitive mode: deny_all, allow_all, allow_list")
	fs.Var(&mcpIntrospectionMode, "mcp-introspection-mode", "mcp introspection mode: deny_all, allow_all, allow_list")
	fs.Var(&mcpReadMode, "mcp-read-mode", "mcp read mode: deny_all, allow_all, allow_list")
	fs.Var(&mcpWriteMode, "mcp-write-mode", "mcp write mode: deny_all, allow_all, allow_list")
	fs.Var(&mcpSensitiveMode, "mcp-sensitive-mode", "mcp sensitive mode: deny_all, allow_all, allow_list")
	fs.Var(&allowCommands, "allow-commands", "comma-separated command allowlist")
	fs.Var(&denyCommands, "deny-commands", "comma-separated command denylist")
	fs.Var(&allowPaths, "allow-paths", "comma-separated path allowlist")
	fs.Var(&denyPaths, "deny-paths", "comma-separated path denylist")
	fs.Var(&allowHosts, "allow-hosts", "comma-separated host allowlist")
	fs.Var(&denyHosts, "deny-hosts", "comma-separated host denylist")
	fs.Var(&allowChannelTools, "allow-channel-tools", "comma-separated channel tool allowlist")
	fs.Var(&denyChannelTools, "deny-channel-tools", "comma-separated channel tool denylist")
	fs.Var(&allowChannelProviders, "allow-channel-providers", "comma-separated channel provider allowlist")
	fs.Var(&denyChannelProviders, "deny-channel-providers", "comma-separated channel provider denylist")
	fs.Var(&allowMCPTools, "allow-mcp-tools", "comma-separated mcp tool allowlist")
	fs.Var(&denyMCPTools, "deny-mcp-tools", "comma-separated mcp tool denylist")
	fs.Var(&allowMCPServers, "allow-mcp-servers", "comma-separated mcp server allowlist")
	fs.Var(&denyMCPServers, "deny-mcp-servers", "comma-separated mcp server denylist")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := requirePermissionTarget(profileID, roomID); err != nil {
		return err
	}
	if err := ensurePermissionTargetExists(ctx, app, profileID, roomID); err != nil {
		return err
	}

	policy, err := app.Repos.ToolPermissions.Resolve(ctx, domain.ProfileID(profileID), domain.RoomID(roomID))
	if err != nil {
		return err
	}
	policy.ProfileID = domain.ProfileID(profileID)
	policy.RoomID = domain.RoomID(roomID)
	policy.UpdatedBy = updatedBy

	if commandsMode.set {
		parsed, err := parseToolPermissionMode(commandsMode.value)
		if err != nil {
			return err
		}
		policy.CommandsMode = parsed
	}
	if pathsMode.set {
		parsed, err := parseToolPermissionMode(pathsMode.value)
		if err != nil {
			return err
		}
		policy.PathsMode = parsed
	}
	if networkMode.set {
		parsed, err := parseToolPermissionMode(networkMode.value)
		if err != nil {
			return err
		}
		policy.NetworkMode = parsed
	}
	if channelIntrospectionMode.set {
		parsed, err := parseToolPermissionMode(channelIntrospectionMode.value)
		if err != nil {
			return err
		}
		policy.ChannelIntrospectionMode = parsed
	}
	if channelReadMode.set {
		parsed, err := parseToolPermissionMode(channelReadMode.value)
		if err != nil {
			return err
		}
		policy.ChannelReadMode = parsed
	}
	if channelWriteMode.set {
		parsed, err := parseToolPermissionMode(channelWriteMode.value)
		if err != nil {
			return err
		}
		policy.ChannelWriteMode = parsed
	}
	if channelSensitiveMode.set {
		parsed, err := parseToolPermissionMode(channelSensitiveMode.value)
		if err != nil {
			return err
		}
		policy.ChannelSensitiveMode = parsed
	}
	if mcpIntrospectionMode.set {
		parsed, err := parseToolPermissionMode(mcpIntrospectionMode.value)
		if err != nil {
			return err
		}
		policy.MCPIntrospectionMode = parsed
	}
	if mcpReadMode.set {
		parsed, err := parseToolPermissionMode(mcpReadMode.value)
		if err != nil {
			return err
		}
		policy.MCPReadMode = parsed
	}
	if mcpWriteMode.set {
		parsed, err := parseToolPermissionMode(mcpWriteMode.value)
		if err != nil {
			return err
		}
		policy.MCPWriteMode = parsed
	}
	if mcpSensitiveMode.set {
		parsed, err := parseToolPermissionMode(mcpSensitiveMode.value)
		if err != nil {
			return err
		}
		policy.MCPSensitiveMode = parsed
	}
	if allowCommands.set {
		policy.AllowedCommands = allowCommands.values
	}
	if denyCommands.set {
		policy.DeniedCommands = denyCommands.values
	}
	if allowPaths.set {
		policy.AllowedPaths = allowPaths.values
	}
	if denyPaths.set {
		policy.DeniedPaths = denyPaths.values
	}
	if allowHosts.set {
		policy.AllowedHosts = allowHosts.values
	}
	if denyHosts.set {
		policy.DeniedHosts = denyHosts.values
	}
	if allowChannelTools.set {
		policy.AllowedChannelTools = allowChannelTools.values
	}
	if denyChannelTools.set {
		policy.DeniedChannelTools = denyChannelTools.values
	}
	if allowChannelProviders.set {
		policy.AllowedChannelProviders = allowChannelProviders.values
	}
	if denyChannelProviders.set {
		policy.DeniedChannelProviders = denyChannelProviders.values
	}
	if allowMCPTools.set {
		policy.AllowedMCPTools = allowMCPTools.values
	}
	if denyMCPTools.set {
		policy.DeniedMCPTools = denyMCPTools.values
	}
	if allowMCPServers.set {
		policy.AllowedMCPServers = allowMCPServers.values
	}
	if denyMCPServers.set {
		policy.DeniedMCPServers = denyMCPServers.values
	}

	if err := app.Repos.ToolPermissions.Upsert(ctx, policy); err != nil {
		return err
	}
	stored, err := app.Repos.ToolPermissions.Get(ctx, domain.ProfileID(profileID), domain.RoomID(roomID))
	if err != nil {
		return err
	}
	return writeToolPermissionPolicy(stdout, stored)
}

func runPermissionsCheck(ctx context.Context, app *runtime.App, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("permissions check", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var profileID string
	var roomID string
	var kind string
	var command string
	var path string
	var host string
	fs.StringVar(&profileID, "profile-id", "", "profile id")
	fs.StringVar(&roomID, "room-id", "", "room id")
	fs.StringVar(&kind, "kind", "", "operation kind: command, read_path, write_path, network")
	fs.StringVar(&command, "command", "", "command to evaluate")
	fs.StringVar(&path, "path", "", "path to evaluate")
	fs.StringVar(&host, "host", "", "host to evaluate")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := requirePermissionTarget(profileID, roomID); err != nil {
		return err
	}
	if err := ensurePermissionTargetExists(ctx, app, profileID, roomID); err != nil {
		return err
	}

	operationKind, err := parseOperationKind(kind)
	if err != nil {
		return err
	}
	authorizer := toolperm.NewAuthorizer(app.Repos.ToolPermissions, nil)
	decision, err := authorizer.Check(ctx, domain.ProfileID(profileID), domain.RoomID(roomID), toolperm.CheckRequest{
		Kind:    operationKind,
		Command: command,
		Path:    path,
		Host:    host,
	})
	if err != nil {
		return err
	}

	type permissionDecisionView struct {
		ProfileID   string `json:"profile_id"`
		RoomID      string `json:"room_id"`
		Allowed     bool   `json:"allowed"`
		Kind        string `json:"kind"`
		Reason      string `json:"reason"`
		MatchedRule string `json:"matched_rule,omitempty"`
		Command     string `json:"command,omitempty"`
		Path        string `json:"path,omitempty"`
		Host        string `json:"host,omitempty"`
	}

	view := permissionDecisionView{
		ProfileID:   profileID,
		RoomID:      roomID,
		Allowed:     decision.Allowed,
		Kind:        string(decision.Kind),
		Reason:      decision.Reason,
		MatchedRule: decision.MatchedRule,
		Command:     command,
		Path:        path,
		Host:        host,
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(view)
}

func runToolingCommand(ctx context.Context, app *runtime.App, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: goclaw tooling <runs|invocations> [flags]")
	}

	switch args[0] {
	case "runs":
		return runToolingRuns(ctx, app, args[1:], stdout)
	case "invocations":
		return runToolingInvocations(ctx, app, args[1:], stdout)
	default:
		return fmt.Errorf("unknown tooling subcommand %q", args[0])
	}
}

func runToolingRuns(ctx context.Context, app *runtime.App, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("tooling runs", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var profileID string
	var roomID string
	var limit int
	fs.StringVar(&profileID, "profile-id", "", "profile id")
	fs.StringVar(&roomID, "room-id", "", "room id")
	fs.IntVar(&limit, "limit", 20, "maximum runs to return")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := requirePermissionTarget(profileID, roomID); err != nil {
		return err
	}
	if err := ensurePermissionTargetExists(ctx, app, profileID, roomID); err != nil {
		return err
	}

	runs, err := app.Repos.ReplyRuns.ListRecentByRoom(
		ctx,
		domain.ProfileID(profileID),
		domain.RoomID(roomID),
		limit,
	)
	if err != nil {
		return err
	}
	return writeReplyRuns(stdout, profileID, roomID, runs)
}

func runToolingInvocations(ctx context.Context, app *runtime.App, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("tooling invocations", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var runID string
	fs.StringVar(&runID, "run-id", "", "reply run id")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(runID) == "" {
		return errors.New("missing required flag --run-id")
	}

	replyRun, err := app.Repos.ReplyRuns.Get(ctx, strings.TrimSpace(runID))
	if err != nil {
		return err
	}
	invocations, err := app.Repos.ToolInvocations.ListByReplyRun(ctx, replyRun.ID)
	if err != nil {
		return err
	}
	return writeToolInvocations(stdout, replyRun, invocations)
}

func requirePermissionTarget(profileID, roomID string) error {
	if strings.TrimSpace(profileID) == "" {
		return errors.New("missing required flag --profile-id")
	}
	if strings.TrimSpace(roomID) == "" {
		return errors.New("missing required flag --room-id")
	}
	return nil
}

func ensurePermissionTargetExists(
	ctx context.Context,
	app *runtime.App,
	profileID string,
	roomID string,
) error {
	if _, err := app.Repos.Profiles.Get(ctx, domain.ProfileID(profileID)); err != nil {
		return err
	}
	if _, err := app.Repos.Rooms.Get(ctx, domain.RoomID(roomID)); err != nil {
		return err
	}
	return nil
}

func parseToolPermissionMode(value string) (domain.ToolPermissionMode, error) {
	switch domain.ToolPermissionMode(strings.TrimSpace(value)) {
	case domain.ToolPermissionDenyAll, domain.ToolPermissionAllowAll, domain.ToolPermissionAllowList:
		return domain.ToolPermissionMode(strings.TrimSpace(value)), nil
	default:
		return "", fmt.Errorf("invalid mode %q (expected deny_all, allow_all, or allow_list)", value)
	}
}

func parseOperationKind(value string) (toolperm.OperationKind, error) {
	switch toolperm.OperationKind(strings.TrimSpace(value)) {
	case toolperm.OperationCommand, toolperm.OperationReadPath, toolperm.OperationWritePath, toolperm.OperationNetwork:
		return toolperm.OperationKind(strings.TrimSpace(value)), nil
	default:
		return "", fmt.Errorf("invalid --kind %q (expected command, read_path, write_path, or network)", value)
	}
}

func defaultPermissionUpdater() string {
	user := strings.TrimSpace(os.Getenv("USER"))
	if user == "" {
		return "goclaw-cli"
	}
	return "cli:" + user
}

func writeToolPermissionPolicy(stdout io.Writer, policy domain.ToolPermissionPolicy) error {
	type toolPermissionPolicyView struct {
		ProfileID                string   `json:"profile_id"`
		RoomID                   string   `json:"room_id"`
		CommandsMode             string   `json:"commands_mode"`
		PathsMode                string   `json:"paths_mode"`
		NetworkMode              string   `json:"network_mode"`
		ChannelIntrospectionMode string   `json:"channel_introspection_mode"`
		ChannelReadMode          string   `json:"channel_read_mode"`
		ChannelWriteMode         string   `json:"channel_write_mode"`
		ChannelSensitiveMode     string   `json:"channel_sensitive_mode"`
		MCPIntrospectionMode     string   `json:"mcp_introspection_mode"`
		MCPReadMode              string   `json:"mcp_read_mode"`
		MCPWriteMode             string   `json:"mcp_write_mode"`
		MCPSensitiveMode         string   `json:"mcp_sensitive_mode"`
		AllowedCommands          []string `json:"allowed_commands,omitempty"`
		DeniedCommands           []string `json:"denied_commands,omitempty"`
		AllowedPaths             []string `json:"allowed_paths,omitempty"`
		DeniedPaths              []string `json:"denied_paths,omitempty"`
		AllowedHosts             []string `json:"allowed_hosts,omitempty"`
		DeniedHosts              []string `json:"denied_hosts,omitempty"`
		AllowedChannelTools      []string `json:"allowed_channel_tools,omitempty"`
		DeniedChannelTools       []string `json:"denied_channel_tools,omitempty"`
		AllowedChannelProviders  []string `json:"allowed_channel_providers,omitempty"`
		DeniedChannelProviders   []string `json:"denied_channel_providers,omitempty"`
		AllowedMCPTools          []string `json:"allowed_mcp_tools,omitempty"`
		DeniedMCPTools           []string `json:"denied_mcp_tools,omitempty"`
		AllowedMCPServers        []string `json:"allowed_mcp_servers,omitempty"`
		DeniedMCPServers         []string `json:"denied_mcp_servers,omitempty"`
		Explicit                 bool     `json:"explicit"`
		UpdatedBy                string   `json:"updated_by,omitempty"`
		CreatedAt                string   `json:"created_at,omitempty"`
		UpdatedAt                string   `json:"updated_at,omitempty"`
	}

	view := toolPermissionPolicyView{
		ProfileID:                string(policy.ProfileID),
		RoomID:                   string(policy.RoomID),
		CommandsMode:             string(policy.CommandsMode),
		PathsMode:                string(policy.PathsMode),
		NetworkMode:              string(policy.NetworkMode),
		ChannelIntrospectionMode: string(policy.ChannelIntrospectionMode),
		ChannelReadMode:          string(policy.ChannelReadMode),
		ChannelWriteMode:         string(policy.ChannelWriteMode),
		ChannelSensitiveMode:     string(policy.ChannelSensitiveMode),
		MCPIntrospectionMode:     string(policy.MCPIntrospectionMode),
		MCPReadMode:              string(policy.MCPReadMode),
		MCPWriteMode:             string(policy.MCPWriteMode),
		MCPSensitiveMode:         string(policy.MCPSensitiveMode),
		AllowedCommands:          append([]string(nil), policy.AllowedCommands...),
		DeniedCommands:           append([]string(nil), policy.DeniedCommands...),
		AllowedPaths:             append([]string(nil), policy.AllowedPaths...),
		DeniedPaths:              append([]string(nil), policy.DeniedPaths...),
		AllowedHosts:             append([]string(nil), policy.AllowedHosts...),
		DeniedHosts:              append([]string(nil), policy.DeniedHosts...),
		AllowedChannelTools:      append([]string(nil), policy.AllowedChannelTools...),
		DeniedChannelTools:       append([]string(nil), policy.DeniedChannelTools...),
		AllowedChannelProviders:  append([]string(nil), policy.AllowedChannelProviders...),
		DeniedChannelProviders:   append([]string(nil), policy.DeniedChannelProviders...),
		AllowedMCPTools:          append([]string(nil), policy.AllowedMCPTools...),
		DeniedMCPTools:           append([]string(nil), policy.DeniedMCPTools...),
		AllowedMCPServers:        append([]string(nil), policy.AllowedMCPServers...),
		DeniedMCPServers:         append([]string(nil), policy.DeniedMCPServers...),
		Explicit:                 policy.Explicit,
		UpdatedBy:                policy.UpdatedBy,
	}
	if !policy.CreatedAt.IsZero() {
		view.CreatedAt = policy.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	if !policy.UpdatedAt.IsZero() {
		view.UpdatedAt = policy.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}

	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(view)
}

func writeReplyRuns(
	stdout io.Writer,
	profileID string,
	roomID string,
	runs []domain.ReplyRun,
) error {
	type replyRunView struct {
		ID               string `json:"id"`
		ConversationID   string `json:"conversation_id"`
		SessionID        string `json:"session_id"`
		InboundMessageID string `json:"inbound_message_id"`
		ModelProvider    string `json:"model_provider"`
		ToolingMode      string `json:"tooling_mode"`
		Status           string `json:"status"`
		ErrorText        string `json:"error_text,omitempty"`
		StartedAt        string `json:"started_at"`
		FinishedAt       string `json:"finished_at,omitempty"`
	}
	type replyRunsView struct {
		ProfileID string         `json:"profile_id"`
		RoomID    string         `json:"room_id"`
		Runs      []replyRunView `json:"runs"`
	}

	view := replyRunsView{
		ProfileID: profileID,
		RoomID:    roomID,
		Runs:      make([]replyRunView, 0, len(runs)),
	}
	for _, run := range runs {
		runView := replyRunView{
			ID:               run.ID,
			ConversationID:   string(run.ConversationID),
			SessionID:        string(run.SessionID),
			InboundMessageID: string(run.InboundMessageID),
			ModelProvider:    run.ModelProvider,
			ToolingMode:      run.ToolingMode,
			Status:           string(run.Status),
			ErrorText:        run.ErrorText,
			StartedAt:        run.StartedAt.UTC().Format(time.RFC3339Nano),
		}
		if run.FinishedAt != nil {
			runView.FinishedAt = run.FinishedAt.UTC().Format(time.RFC3339Nano)
		}
		view.Runs = append(view.Runs, runView)
	}

	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(view)
}

func writeToolInvocations(
	stdout io.Writer,
	replyRun domain.ReplyRun,
	invocations []domain.ToolInvocation,
) error {
	type toolInvocationView struct {
		ID           string `json:"id"`
		Iteration    int    `json:"iteration"`
		ToolCallID   string `json:"tool_call_id"`
		ToolName     string `json:"tool_name"`
		ToolSource   string `json:"tool_source,omitempty"`
		Provider     string `json:"provider,omitempty"`
		CapabilityID string `json:"capability_id,omitempty"`
		ArgsJSON     string `json:"args_json"`
		DecisionJSON string `json:"decision_json"`
		Status       string `json:"status"`
		ResultJSON   string `json:"result_json,omitempty"`
		ErrorText    string `json:"error_text,omitempty"`
		StartedAt    string `json:"started_at"`
		FinishedAt   string `json:"finished_at,omitempty"`
	}
	type toolInvocationsView struct {
		RunID       string               `json:"run_id"`
		ProfileID   string               `json:"profile_id"`
		RoomID      string               `json:"room_id"`
		Invocations []toolInvocationView `json:"invocations"`
	}

	view := toolInvocationsView{
		RunID:       replyRun.ID,
		ProfileID:   string(replyRun.ProfileID),
		RoomID:      string(replyRun.RoomID),
		Invocations: make([]toolInvocationView, 0, len(invocations)),
	}
	for _, invocation := range invocations {
		invocationView := toolInvocationView{
			ID:           invocation.ID,
			Iteration:    invocation.Iteration,
			ToolCallID:   invocation.ToolCallID,
			ToolName:     invocation.ToolName,
			ToolSource:   invocation.ToolSource,
			Provider:     string(invocation.Provider),
			CapabilityID: invocation.CapabilityID,
			ArgsJSON:     invocation.ArgsJSON,
			DecisionJSON: invocation.DecisionJSON,
			Status:       string(invocation.Status),
			ResultJSON:   invocation.ResultJSON,
			ErrorText:    invocation.ErrorText,
			StartedAt:    invocation.StartedAt.UTC().Format(time.RFC3339Nano),
		}
		if invocation.FinishedAt != nil {
			invocationView.FinishedAt = invocation.FinishedAt.UTC().Format(time.RFC3339Nano)
		}
		view.Invocations = append(view.Invocations, invocationView)
	}

	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(view)
}

type optionalStringFlag struct {
	value string
	set   bool
}

func (f *optionalStringFlag) String() string {
	return f.value
}

func (f *optionalStringFlag) Set(value string) error {
	f.value = strings.TrimSpace(value)
	f.set = true
	return nil
}

type optionalCSVFlag struct {
	values []string
	set    bool
}

func (f *optionalCSVFlag) String() string {
	return strings.Join(f.values, ",")
}

func (f *optionalCSVFlag) Set(value string) error {
	f.values = splitCSV(value)
	f.set = true
	return nil
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}
