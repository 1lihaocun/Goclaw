package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os/user"
	"strings"
	"time"

	"goclaw/internal/domain"
	"goclaw/internal/runtime"
	sqlitestore "goclaw/internal/store/sqlite"
)

func runConversationsCommand(ctx context.Context, app *runtime.App, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: goclaw conversations <list|get|create|activate|settings> [flags]")
	}

	switch args[0] {
	case "list":
		return runConversationsList(ctx, app, args[1:], stdout)
	case "get":
		return runConversationsGet(ctx, app, args[1:], stdout)
	case "create":
		return runConversationsCreate(ctx, app, args[1:], stdout)
	case "activate":
		return runConversationsActivate(ctx, app, args[1:], stdout)
	case "settings":
		return runConversationSettingsCommand(ctx, app, args[1:], stdout)
	default:
		return fmt.Errorf("unknown conversations subcommand %q", args[0])
	}
}

func runConversationsList(ctx context.Context, app *runtime.App, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("conversations list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var profileID string
	var roomID string
	fs.StringVar(&profileID, "profile-id", "", "profile id")
	fs.StringVar(&roomID, "room-id", "", "room id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := requireConversationRoomTarget(profileID, roomID); err != nil {
		return err
	}
	if err := ensureConversationRoomExists(ctx, app, profileID, roomID); err != nil {
		return err
	}

	conversations, err := app.Repos.Conversations.ListByProfileRoom(ctx, domain.ProfileID(profileID), domain.RoomID(roomID))
	if err != nil {
		return err
	}
	session, err := app.Repos.RoomSessions.GetByProfileAndRoom(ctx, domain.ProfileID(profileID), domain.RoomID(roomID))
	if err != nil && !errors.Is(err, sqlitestore.ErrNotFound) {
		return err
	}
	return writeConversationList(stdout, profileID, roomID, session.ActiveConversationID, conversations)
}

func runConversationsGet(ctx context.Context, app *runtime.App, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("conversations get", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var profileID string
	var roomID string
	var slug string
	fs.StringVar(&profileID, "profile-id", "", "profile id")
	fs.StringVar(&roomID, "room-id", "", "room id")
	fs.StringVar(&slug, "slug", "", "conversation slug")
	if err := fs.Parse(args); err != nil {
		return err
	}
	conversation, activeID, err := resolveConversationBySlug(ctx, app, profileID, roomID, slug)
	if err != nil {
		return err
	}
	return writeConversation(stdout, conversation, activeID == conversation.ID)
}

func runConversationsCreate(ctx context.Context, app *runtime.App, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("conversations create", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var profileID string
	var roomID string
	var slug string
	var title string
	fs.StringVar(&profileID, "profile-id", "", "profile id")
	fs.StringVar(&roomID, "room-id", "", "room id")
	fs.StringVar(&slug, "slug", "", "conversation slug")
	fs.StringVar(&title, "title", "", "conversation title")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := requireConversationTarget(profileID, roomID, slug); err != nil {
		return err
	}
	if err := ensureConversationRoomExists(ctx, app, profileID, roomID); err != nil {
		return err
	}

	conversation := domain.Conversation{
		ID:        buildCLIConversationID(domain.ProfileID(profileID), domain.RoomID(roomID), slug),
		ProfileID: domain.ProfileID(profileID),
		RoomID:    domain.RoomID(roomID),
		Slug:      strings.TrimSpace(slug),
		Title:     conversationTitle(title, slug),
		Status:    domain.ConversationStatusActive,
	}
	if err := app.Repos.Conversations.Upsert(ctx, conversation); err != nil {
		return err
	}
	stored, err := app.Repos.Conversations.Get(ctx, conversation.ID)
	if err != nil {
		return err
	}
	return writeConversation(stdout, stored, false)
}

func runConversationsActivate(ctx context.Context, app *runtime.App, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("conversations activate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var profileID string
	var roomID string
	var slug string
	fs.StringVar(&profileID, "profile-id", "", "profile id")
	fs.StringVar(&roomID, "room-id", "", "room id")
	fs.StringVar(&slug, "slug", "", "conversation slug")
	if err := fs.Parse(args); err != nil {
		return err
	}
	conversation, _, err := resolveConversationBySlug(ctx, app, profileID, roomID, slug)
	if err != nil {
		return err
	}

	session, err := ensureConversationRoomSession(ctx, app, domain.ProfileID(profileID), domain.RoomID(roomID))
	if err != nil {
		return err
	}
	session.ActiveConversationID = conversation.ID
	if err := app.Repos.RoomSessions.Upsert(ctx, session); err != nil {
		return err
	}
	return writeConversation(stdout, conversation, true)
}

func runConversationSettingsCommand(ctx context.Context, app *runtime.App, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: goclaw conversations settings <get|set> [flags]")
	}
	switch args[0] {
	case "get":
		return runConversationSettingsGet(ctx, app, args[1:], stdout)
	case "set":
		return runConversationSettingsSet(ctx, app, args[1:], stdout)
	default:
		return fmt.Errorf("unknown conversations settings subcommand %q", args[0])
	}
}

func runConversationSettingsGet(ctx context.Context, app *runtime.App, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("conversations settings get", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var profileID string
	var roomID string
	var slug string
	fs.StringVar(&profileID, "profile-id", "", "profile id")
	fs.StringVar(&roomID, "room-id", "", "room id")
	fs.StringVar(&slug, "slug", "", "conversation slug")
	if err := fs.Parse(args); err != nil {
		return err
	}
	conversation, _, err := resolveConversationBySlug(ctx, app, profileID, roomID, slug)
	if err != nil {
		return err
	}
	settings, err := app.Repos.ConversationSettings.Resolve(ctx, conversation.ID)
	if err != nil {
		return err
	}
	return writeConversationSettings(stdout, conversation, settings)
}

func runConversationSettingsSet(ctx context.Context, app *runtime.App, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("conversations settings set", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var profileID string
	var roomID string
	var slug string
	var enabled optionalStringFlag
	var allowProviderMemory optionalStringFlag
	var allowMarkdownMemory optionalStringFlag
	var writeAssistantToPrivate optionalStringFlag
	var journalMode optionalStringFlag
	var readScopes optionalCSVFlag
	var writeScopes optionalCSVFlag
	var updatedBy string

	fs.StringVar(&profileID, "profile-id", "", "profile id")
	fs.StringVar(&roomID, "room-id", "", "room id")
	fs.StringVar(&slug, "slug", "", "conversation slug")
	fs.Var(&enabled, "enabled", "optional bool")
	fs.Var(&allowProviderMemory, "allow-provider-memory", "optional bool")
	fs.Var(&allowMarkdownMemory, "allow-markdown-memory", "optional bool")
	fs.Var(&writeAssistantToPrivate, "write-assistant-to-person-private", "optional bool")
	fs.Var(&journalMode, "journal-mode", "off|provider_only|markdown_only|both")
	fs.Var(&readScopes, "read-scopes", "comma-separated read scopes")
	fs.Var(&writeScopes, "write-scopes", "comma-separated write scopes")
	fs.StringVar(&updatedBy, "updated-by", defaultConversationUpdater(), "audit actor")
	if err := fs.Parse(args); err != nil {
		return err
	}

	conversation, _, err := resolveConversationBySlug(ctx, app, profileID, roomID, slug)
	if err != nil {
		return err
	}
	settings, err := app.Repos.ConversationSettings.Resolve(ctx, conversation.ID)
	if err != nil {
		return err
	}

	if enabled.set {
		value, err := parseBoolString(enabled.value, "--enabled")
		if err != nil {
			return err
		}
		settings.Enabled = value
	}
	if allowProviderMemory.set {
		value, err := parseBoolString(allowProviderMemory.value, "--allow-provider-memory")
		if err != nil {
			return err
		}
		settings.AllowProviderMemory = value
	}
	if allowMarkdownMemory.set {
		value, err := parseBoolString(allowMarkdownMemory.value, "--allow-markdown-memory")
		if err != nil {
			return err
		}
		settings.AllowMarkdownMemory = value
	}
	if writeAssistantToPrivate.set {
		value, err := parseBoolString(writeAssistantToPrivate.value, "--write-assistant-to-person-private")
		if err != nil {
			return err
		}
		settings.WriteAssistantToPersonPrivate = value
	}
	if journalMode.set {
		value, err := parseConversationJournalMode(journalMode.value)
		if err != nil {
			return err
		}
		settings.JournalMode = value
	}
	if readScopes.set {
		if err := validateConversationScopes(readScopes.values, "--read-scopes"); err != nil {
			return err
		}
		settings.ReadScopes = append([]string(nil), readScopes.values...)
	}
	if writeScopes.set {
		if err := validateConversationScopes(writeScopes.values, "--write-scopes"); err != nil {
			return err
		}
		settings.WriteScopes = append([]string(nil), writeScopes.values...)
	}
	settings.UpdatedBy = updatedBy
	if err := app.Repos.ConversationSettings.Upsert(ctx, settings); err != nil {
		return err
	}
	stored, err := app.Repos.ConversationSettings.Get(ctx, conversation.ID)
	if err != nil {
		return err
	}
	return writeConversationSettings(stdout, conversation, stored)
}

func resolveConversationBySlug(
	ctx context.Context,
	app *runtime.App,
	profileID string,
	roomID string,
	slug string,
) (domain.Conversation, domain.ConversationID, error) {
	if err := requireConversationTarget(profileID, roomID, slug); err != nil {
		return domain.Conversation{}, "", err
	}
	if err := ensureConversationRoomExists(ctx, app, profileID, roomID); err != nil {
		return domain.Conversation{}, "", err
	}
	conversation, err := app.Repos.Conversations.GetByProfileRoomSlug(
		ctx,
		domain.ProfileID(profileID),
		domain.RoomID(roomID),
		strings.TrimSpace(slug),
	)
	if err != nil {
		return domain.Conversation{}, "", err
	}
	session, err := app.Repos.RoomSessions.GetByProfileAndRoom(ctx, domain.ProfileID(profileID), domain.RoomID(roomID))
	if err != nil && !errors.Is(err, sqlitestore.ErrNotFound) {
		return domain.Conversation{}, "", err
	}
	return conversation, session.ActiveConversationID, nil
}

func requireConversationRoomTarget(profileID, roomID string) error {
	if strings.TrimSpace(profileID) == "" {
		return errors.New("missing required flag --profile-id")
	}
	if strings.TrimSpace(roomID) == "" {
		return errors.New("missing required flag --room-id")
	}
	return nil
}

func requireConversationTarget(profileID, roomID, slug string) error {
	if err := requireConversationRoomTarget(profileID, roomID); err != nil {
		return err
	}
	return validateConversationSlug(slug)
}

func ensureConversationRoomExists(ctx context.Context, app *runtime.App, profileID, roomID string) error {
	if _, err := app.Repos.Profiles.Get(ctx, domain.ProfileID(profileID)); err != nil {
		return err
	}
	if _, err := app.Repos.Rooms.Get(ctx, domain.RoomID(roomID)); err != nil {
		return err
	}
	return nil
}

func ensureConversationRoomSession(
	ctx context.Context,
	app *runtime.App,
	profileID domain.ProfileID,
	roomID domain.RoomID,
) (domain.RoomSession, error) {
	session, err := app.Repos.RoomSessions.GetByProfileAndRoom(ctx, profileID, roomID)
	if err == nil {
		return session, nil
	}
	if !errors.Is(err, sqlitestore.ErrNotFound) {
		return domain.RoomSession{}, err
	}
	room, err := app.Repos.Rooms.Get(ctx, roomID)
	if err != nil {
		return domain.RoomSession{}, err
	}
	session = domain.RoomSession{
		ID:            buildCLIRoomSessionID(profileID, room.Provider, roomID),
		ProfileID:     profileID,
		RoomID:        roomID,
		Status:        "active",
		LastMessageAt: time.Now().UTC(),
	}
	if err := app.Repos.RoomSessions.Upsert(ctx, session); err != nil {
		return domain.RoomSession{}, err
	}
	return session, nil
}

func writeConversationList(
	stdout io.Writer,
	profileID string,
	roomID string,
	activeConversationID domain.ConversationID,
	conversations []domain.Conversation,
) error {
	type conversationView struct {
		ID        string `json:"id"`
		Slug      string `json:"slug"`
		Title     string `json:"title"`
		Status    string `json:"status"`
		Active    bool   `json:"active"`
		CreatedAt string `json:"created_at,omitempty"`
		UpdatedAt string `json:"updated_at,omitempty"`
	}
	type conversationListView struct {
		ProfileID            string             `json:"profile_id"`
		RoomID               string             `json:"room_id"`
		ActiveConversationID string             `json:"active_conversation_id,omitempty"`
		Conversations        []conversationView `json:"conversations"`
	}

	view := conversationListView{
		ProfileID:            profileID,
		RoomID:               roomID,
		ActiveConversationID: string(activeConversationID),
		Conversations:        make([]conversationView, 0, len(conversations)),
	}
	for _, conversation := range conversations {
		item := conversationView{
			ID:     string(conversation.ID),
			Slug:   conversation.Slug,
			Title:  conversation.Title,
			Status: string(conversation.Status),
			Active: conversation.ID == activeConversationID,
		}
		if !conversation.CreatedAt.IsZero() {
			item.CreatedAt = conversation.CreatedAt.UTC().Format(time.RFC3339Nano)
		}
		if !conversation.UpdatedAt.IsZero() {
			item.UpdatedAt = conversation.UpdatedAt.UTC().Format(time.RFC3339Nano)
		}
		view.Conversations = append(view.Conversations, item)
	}
	return writeJSON(stdout, view)
}

func writeConversation(stdout io.Writer, conversation domain.Conversation, active bool) error {
	type conversationView struct {
		ID        string `json:"id"`
		ProfileID string `json:"profile_id"`
		RoomID    string `json:"room_id"`
		Slug      string `json:"slug"`
		Title     string `json:"title"`
		Status    string `json:"status"`
		Active    bool   `json:"active"`
		CreatedAt string `json:"created_at,omitempty"`
		UpdatedAt string `json:"updated_at,omitempty"`
	}
	view := conversationView{
		ID:        string(conversation.ID),
		ProfileID: string(conversation.ProfileID),
		RoomID:    string(conversation.RoomID),
		Slug:      conversation.Slug,
		Title:     conversation.Title,
		Status:    string(conversation.Status),
		Active:    active,
	}
	if !conversation.CreatedAt.IsZero() {
		view.CreatedAt = conversation.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	if !conversation.UpdatedAt.IsZero() {
		view.UpdatedAt = conversation.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	return writeJSON(stdout, view)
}

func writeConversationSettings(stdout io.Writer, conversation domain.Conversation, settings domain.ConversationSettings) error {
	type conversationSettingsView struct {
		ConversationID                string   `json:"conversation_id"`
		ProfileID                     string   `json:"profile_id"`
		RoomID                        string   `json:"room_id"`
		Slug                          string   `json:"slug"`
		Enabled                       bool     `json:"enabled"`
		AllowProviderMemory           bool     `json:"allow_provider_memory"`
		AllowMarkdownMemory           bool     `json:"allow_markdown_memory"`
		ReadScopes                    []string `json:"read_scopes"`
		WriteScopes                   []string `json:"write_scopes"`
		WriteAssistantToPersonPrivate bool     `json:"write_assistant_to_person_private"`
		JournalMode                   string   `json:"journal_mode"`
		Explicit                      bool     `json:"explicit"`
		UpdatedBy                     string   `json:"updated_by,omitempty"`
		CreatedAt                     string   `json:"created_at,omitempty"`
		UpdatedAt                     string   `json:"updated_at,omitempty"`
	}
	view := conversationSettingsView{
		ConversationID:                string(settings.ConversationID),
		ProfileID:                     string(conversation.ProfileID),
		RoomID:                        string(conversation.RoomID),
		Slug:                          conversation.Slug,
		Enabled:                       settings.Enabled,
		AllowProviderMemory:           settings.AllowProviderMemory,
		AllowMarkdownMemory:           settings.AllowMarkdownMemory,
		ReadScopes:                    append([]string(nil), settings.ReadScopes...),
		WriteScopes:                   append([]string(nil), settings.WriteScopes...),
		WriteAssistantToPersonPrivate: settings.WriteAssistantToPersonPrivate,
		JournalMode:                   string(settings.JournalMode),
		Explicit:                      settings.Explicit,
		UpdatedBy:                     settings.UpdatedBy,
	}
	if !settings.CreatedAt.IsZero() {
		view.CreatedAt = settings.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	if !settings.UpdatedAt.IsZero() {
		view.UpdatedAt = settings.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	return writeJSON(stdout, view)
}

func validateConversationSlug(value string) error {
	slug := strings.TrimSpace(value)
	if slug == "" {
		return errors.New("missing required flag --slug")
	}
	if slug == "." || slug == ".." || strings.Contains(slug, "/") || strings.Contains(slug, "\\") {
		return fmt.Errorf("invalid conversation slug %q", value)
	}
	return nil
}

func conversationTitle(title, slug string) string {
	if trimmed := strings.TrimSpace(title); trimmed != "" {
		return trimmed
	}
	return strings.TrimSpace(slug)
}

func buildCLIConversationID(profileID domain.ProfileID, roomID domain.RoomID, slug string) domain.ConversationID {
	return domain.ConversationID(fmt.Sprintf("conversation:%s:%s:%s", profileID, roomID, strings.TrimSpace(slug)))
}

func buildCLIRoomSessionID(profileID domain.ProfileID, provider domain.Provider, roomID domain.RoomID) domain.SessionID {
	providerName := strings.TrimSpace(string(provider))
	if providerName == "" {
		providerName = "unknown"
	}
	return domain.SessionID(fmt.Sprintf("session:%s:%s:%s", providerName, profileID, roomID))
}

func parseBoolString(value, flagName string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "yes", "on":
		return true, nil
	case "false", "0", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("parse %s: invalid bool %q", flagName, value)
	}
}

func parseConversationJournalMode(value string) (domain.ConversationJournalMode, error) {
	switch domain.ConversationJournalMode(strings.TrimSpace(value)) {
	case domain.ConversationJournalOff,
		domain.ConversationJournalProviderOnly,
		domain.ConversationJournalMarkdownOnly,
		domain.ConversationJournalBoth:
		return domain.ConversationJournalMode(strings.TrimSpace(value)), nil
	default:
		return "", fmt.Errorf("invalid conversation journal mode %q", value)
	}
}

func validateConversationScopes(scopes []string, flagName string) error {
	for _, scope := range scopes {
		switch strings.TrimSpace(scope) {
		case domain.ConversationScopeConversation,
			domain.ConversationScopeRoom,
			domain.ConversationScopePersonSummary,
			domain.ConversationScopePersonPrivate:
		default:
			return fmt.Errorf("invalid %s scope %q", flagName, scope)
		}
	}
	return nil
}

func defaultConversationUpdater() string {
	currentUser, err := user.Current()
	if err != nil {
		return "goclaw-cli"
	}
	name := strings.TrimSpace(currentUser.Username)
	if name == "" {
		return "goclaw-cli"
	}
	return "cli:" + name
}

func mustDecodeJSON[T any](data string) (T, error) {
	var value T
	err := json.Unmarshal([]byte(data), &value)
	return value, err
}
