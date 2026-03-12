package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"goclaw/internal/domain"
	"goclaw/internal/runtime"
	toolruntime "goclaw/internal/tools"
)

func runToolsCommand(ctx context.Context, app *runtime.App, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: goclaw tools <exec|read|write|fetch> [flags]")
	}

	switch args[0] {
	case "exec":
		return runToolsExec(ctx, app, args[1:], stdout)
	case "read":
		return runToolsRead(ctx, app, args[1:], stdout)
	case "write":
		return runToolsWrite(ctx, app, args[1:], stdout)
	case "fetch":
		return runToolsFetch(ctx, app, args[1:], stdout)
	default:
		return fmt.Errorf("unknown tools subcommand %q", args[0])
	}
}

func runToolsExec(ctx context.Context, app *runtime.App, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("tools exec", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var profileID string
	var roomID string
	var workdir string
	var stdin string
	var timeout string
	fs.StringVar(&profileID, "profile-id", "", "profile id")
	fs.StringVar(&roomID, "room-id", "", "room id")
	fs.StringVar(&workdir, "workdir", "", "command working directory")
	fs.StringVar(&stdin, "stdin", "", "stdin content")
	fs.StringVar(&timeout, "timeout", "10s", "command timeout")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := requirePermissionTarget(profileID, roomID); err != nil {
		return err
	}
	if err := ensurePermissionTargetExists(ctx, app, profileID, roomID); err != nil {
		return err
	}
	commandArgs := fs.Args()
	if len(commandArgs) == 0 {
		return errors.New("missing command after flags")
	}

	parsedTimeout, err := time.ParseDuration(strings.TrimSpace(timeout))
	if err != nil {
		return fmt.Errorf("parse --timeout: %w", err)
	}

	localRuntime := toolruntime.NewLocalRuntime(app.Repos.ToolPermissions, nil)
	result, err := localRuntime.RunCommand(ctx, domain.ProfileID(profileID), domain.RoomID(roomID), toolruntime.CommandRequest{
		Program: commandArgs[0],
		Args:    append([]string(nil), commandArgs[1:]...),
		Workdir: workdir,
		Stdin:   stdin,
		Timeout: parsedTimeout,
	})
	if err != nil {
		return err
	}
	return writeJSON(stdout, result)
}

func runToolsRead(ctx context.Context, app *runtime.App, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("tools read", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var profileID string
	var roomID string
	var path string
	fs.StringVar(&profileID, "profile-id", "", "profile id")
	fs.StringVar(&roomID, "room-id", "", "room id")
	fs.StringVar(&path, "path", "", "file path")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := requirePermissionTarget(profileID, roomID); err != nil {
		return err
	}
	if err := ensurePermissionTargetExists(ctx, app, profileID, roomID); err != nil {
		return err
	}

	localRuntime := toolruntime.NewLocalRuntime(app.Repos.ToolPermissions, nil)
	result, err := localRuntime.ReadFile(ctx, domain.ProfileID(profileID), domain.RoomID(roomID), path)
	if err != nil {
		return err
	}
	return writeJSON(stdout, result)
}

func runToolsWrite(ctx context.Context, app *runtime.App, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("tools write", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var profileID string
	var roomID string
	var path string
	var content string
	var appendMode bool
	var mkdir bool
	fs.StringVar(&profileID, "profile-id", "", "profile id")
	fs.StringVar(&roomID, "room-id", "", "room id")
	fs.StringVar(&path, "path", "", "file path")
	fs.StringVar(&content, "content", "", "file content")
	fs.BoolVar(&appendMode, "append", false, "append instead of overwrite")
	fs.BoolVar(&mkdir, "mkdir", false, "create parent directories")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := requirePermissionTarget(profileID, roomID); err != nil {
		return err
	}
	if err := ensurePermissionTargetExists(ctx, app, profileID, roomID); err != nil {
		return err
	}

	localRuntime := toolruntime.NewLocalRuntime(app.Repos.ToolPermissions, nil)
	result, err := localRuntime.WriteFile(ctx, domain.ProfileID(profileID), domain.RoomID(roomID), toolruntime.WriteFileRequest{
		Path:       path,
		Content:    content,
		Append:     appendMode,
		CreateDirs: mkdir,
	})
	if err != nil {
		return err
	}
	return writeJSON(stdout, result)
}

func runToolsFetch(ctx context.Context, app *runtime.App, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("tools fetch", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var profileID string
	var roomID string
	var rawURL string
	var timeout string
	var maxBytes int64
	fs.StringVar(&profileID, "profile-id", "", "profile id")
	fs.StringVar(&roomID, "room-id", "", "room id")
	fs.StringVar(&rawURL, "url", "", "target url")
	fs.StringVar(&timeout, "timeout", "10s", "fetch timeout")
	fs.Int64Var(&maxBytes, "max-bytes", 64<<10, "maximum response bytes")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := requirePermissionTarget(profileID, roomID); err != nil {
		return err
	}
	if err := ensurePermissionTargetExists(ctx, app, profileID, roomID); err != nil {
		return err
	}

	parsedTimeout, err := time.ParseDuration(strings.TrimSpace(timeout))
	if err != nil {
		return fmt.Errorf("parse --timeout: %w", err)
	}

	localRuntime := toolruntime.NewLocalRuntime(app.Repos.ToolPermissions, nil)
	result, err := localRuntime.FetchURL(ctx, domain.ProfileID(profileID), domain.RoomID(roomID), toolruntime.FetchRequest{
		URL:      rawURL,
		Timeout:  parsedTimeout,
		MaxBytes: maxBytes,
	})
	if err != nil {
		return err
	}
	return writeJSON(stdout, result)
}

func writeJSON(stdout io.Writer, value any) error {
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
