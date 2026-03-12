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
	"goclaw/internal/memory"
	"goclaw/internal/runtime"
	workspacectx "goclaw/internal/workspace"
)

func runMemoryCommand(ctx context.Context, app *runtime.App, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: goclaw memory <promote|jobs|compact|review> [flags]")
	}

	switch args[0] {
	case "promote":
		return runMemoryPromote(ctx, app, args[1:], stdout)
	case "jobs":
		return runMemoryJobsCommand(ctx, app, args[1:], stdout)
	case "compact":
		return runMemoryCompactCommand(ctx, app, args[1:], stdout)
	case "review":
		return runMemoryReviewCommand(ctx, app, args[1:], stdout)
	default:
		return fmt.Errorf("unknown memory subcommand %q", args[0])
	}
}

func runMemoryPromote(ctx context.Context, app *runtime.App, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("memory promote", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var profileID string
	var roomID string
	var personID string
	var slug string
	var scopeText string
	var content string
	var updatedBy string
	var deferWrite bool

	fs.StringVar(&profileID, "profile-id", "", "profile id")
	fs.StringVar(&roomID, "room-id", "", "room id")
	fs.StringVar(&personID, "person-id", "", "person id")
	fs.StringVar(&slug, "slug", "", "conversation slug")
	fs.StringVar(&scopeText, "scope", "", "scope: conversation, room, person_summary, person_private")
	fs.StringVar(&content, "content", "", "durable memory fact to append")
	fs.StringVar(&updatedBy, "updated-by", defaultConversationUpdater(), "audit actor")
	fs.BoolVar(&deferWrite, "defer", false, "enqueue the durable promotion into memory_jobs instead of writing immediately")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if strings.TrimSpace(app.Config.Workspace.Root) == "" {
		return errors.New("workspace markdown memory is unavailable; set GOCLAW_WORKSPACE_ROOT or workspace.root")
	}

	scope, err := parseMemoryPromoteScope(scopeText)
	if err != nil {
		return err
	}
	if strings.TrimSpace(content) == "" {
		return errors.New("missing required flag --content")
	}
	if err := ensureMemoryPromoteTargetExists(ctx, app, scope, profileID, roomID, personID, slug); err != nil {
		return err
	}

	durableInput := workspacectx.DurableWriteInput{
		ProfileID:        domain.ProfileID(strings.TrimSpace(profileID)),
		PersonID:         domain.PersonID(strings.TrimSpace(personID)),
		RoomID:           domain.RoomID(strings.TrimSpace(roomID)),
		ConversationSlug: strings.TrimSpace(slug),
		Scope:            scope,
		Facts:            []string{content},
		UpdatedBy:        strings.TrimSpace(updatedBy),
	}
	if scope == memory.ScopeConversation {
		durableInput.ConversationID = buildCLIConversationID(
			domain.ProfileID(strings.TrimSpace(profileID)),
			domain.RoomID(strings.TrimSpace(roomID)),
			strings.TrimSpace(slug),
		)
	}

	type memoryPromoteView struct {
		Mode             string   `json:"mode"`
		Scope            string   `json:"scope"`
		ProfileID        string   `json:"profile_id,omitempty"`
		RoomID           string   `json:"room_id,omitempty"`
		PersonID         string   `json:"person_id,omitempty"`
		ConversationSlug string   `json:"conversation_slug,omitempty"`
		Path             string   `json:"path,omitempty"`
		AddedFacts       []string `json:"added_facts,omitempty"`
		SkippedFacts     []string `json:"skipped_facts,omitempty"`
		JobID            string   `json:"job_id,omitempty"`
		JobStatus        string   `json:"job_status,omitempty"`
		UpdatedBy        string   `json:"updated_by,omitempty"`
	}

	if deferWrite {
		sourceMessage, err := resolveMemoryPromoteSourceMessage(ctx, app, scope, profileID, roomID, personID, slug)
		if err != nil {
			return err
		}
		job, err := runtime.BuildDurablePromotionMemoryJob(time.Now().UTC(), sourceMessage.ID, durableInput)
		if err != nil {
			return err
		}
		if err := app.Repos.MemoryJobs.Insert(ctx, job); err != nil {
			return err
		}
		return writeJSON(stdout, memoryPromoteView{
			Mode:             "queued",
			Scope:            string(scope),
			ProfileID:        strings.TrimSpace(profileID),
			RoomID:           strings.TrimSpace(roomID),
			PersonID:         strings.TrimSpace(personID),
			ConversationSlug: strings.TrimSpace(slug),
			JobID:            job.ID,
			JobStatus:        string(job.Status),
			UpdatedBy:        strings.TrimSpace(updatedBy),
		})
	}

	loader := workspacectx.NewLoader(app.Config.Workspace.Root)
	result, err := loader.AppendDurable(ctx, durableInput)
	if err != nil {
		return err
	}

	view := memoryPromoteView{
		Mode:             "direct",
		Scope:            string(scope),
		ProfileID:        strings.TrimSpace(profileID),
		RoomID:           strings.TrimSpace(roomID),
		PersonID:         strings.TrimSpace(personID),
		ConversationSlug: strings.TrimSpace(slug),
		Path:             result.Path,
		AddedFacts:       append([]string(nil), result.AddedFacts...),
		SkippedFacts:     append([]string(nil), result.SkippedFacts...),
		UpdatedBy:        strings.TrimSpace(updatedBy),
	}
	return writeJSON(stdout, view)
}

func runMemoryJobsCommand(ctx context.Context, app *runtime.App, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: goclaw memory jobs <run-once> [flags]")
	}
	switch args[0] {
	case "run-once":
		worker := runtime.NewMemoryJobWorker(
			app.Repos,
			app.Memory,
			workspacectx.NewLoader(app.Config.Workspace.Root),
			nil,
		)
		processed, err := worker.DrainOnce(ctx)
		if err != nil {
			return err
		}
		return writeJSON(stdout, map[string]int{"processed": processed})
	default:
		return fmt.Errorf("unknown memory jobs subcommand %q", args[0])
	}
}

func runMemoryCompactCommand(ctx context.Context, app *runtime.App, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: goclaw memory compact <run-once> [flags]")
	}
	switch args[0] {
	case "run-once":
		worker := runtime.NewMemoryCompactionWorker(
			app.Repos,
			workspacectx.NewLoader(app.Config.Workspace.Root),
			app.Config.Memory.AutoPromote,
			nil,
		)
		enqueued, err := worker.DrainOnce(ctx)
		if err != nil {
			return err
		}
		return writeJSON(stdout, map[string]int{"enqueued": enqueued})
	default:
		return fmt.Errorf("unknown memory compact subcommand %q", args[0])
	}
}

func runMemoryReviewCommand(ctx context.Context, app *runtime.App, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: goclaw memory review <list|approve|reject> [flags]")
	}
	switch args[0] {
	case "list":
		return runMemoryReviewList(ctx, app, args[1:], stdout)
	case "approve":
		return runMemoryReviewApprove(ctx, app, args[1:], stdout)
	case "reject":
		return runMemoryReviewReject(ctx, app, args[1:], stdout)
	default:
		return fmt.Errorf("unknown memory review subcommand %q", args[0])
	}
}

func runMemoryReviewList(ctx context.Context, app *runtime.App, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("memory review list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var limit int
	fs.IntVar(&limit, "limit", 20, "max number of review jobs")
	if err := fs.Parse(args); err != nil {
		return err
	}

	jobs, err := app.Repos.MemoryJobs.ListByStatus(ctx, domain.MemoryJobReview, limit)
	if err != nil {
		return err
	}

	type reviewView struct {
		JobID            string                         `json:"job_id"`
		Status           string                         `json:"status"`
		Scope            string                         `json:"scope"`
		TargetType       string                         `json:"target_type"`
		TargetID         string                         `json:"target_id"`
		ConversationSlug string                         `json:"conversation_slug,omitempty"`
		Facts            []string                       `json:"facts"`
		Conflicts        []workspacectx.DurableConflict `json:"conflicts"`
		CreatedAt        time.Time                      `json:"created_at"`
	}

	views := make([]reviewView, 0, len(jobs))
	for _, job := range jobs {
		if job.Kind != runtime.MemoryJobKindMarkdownReview {
			continue
		}
		var payload runtime.DurableReviewJobPayload
		if err := json.Unmarshal([]byte(job.PayloadJSON), &payload); err != nil {
			return fmt.Errorf("decode review payload %s: %w", job.ID, err)
		}
		views = append(views, reviewView{
			JobID:            job.ID,
			Status:           string(job.Status),
			Scope:            string(payload.Scope.Kind),
			TargetType:       job.TargetType,
			TargetID:         job.TargetID,
			ConversationSlug: payload.ConversationSlug,
			Facts:            append([]string(nil), payload.Facts...),
			Conflicts:        append([]workspacectx.DurableConflict(nil), payload.Conflicts...),
			CreatedAt:        job.CreatedAt,
		})
	}
	return writeJSON(stdout, views)
}

func runMemoryReviewApprove(ctx context.Context, app *runtime.App, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("memory review approve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var jobID string
	fs.StringVar(&jobID, "job-id", "", "review job id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(jobID) == "" {
		return errors.New("missing required flag --job-id")
	}

	job, payload, err := loadReviewJob(ctx, app, jobID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(app.Config.Workspace.Root) == "" {
		return errors.New("workspace markdown memory is unavailable; set GOCLAW_WORKSPACE_ROOT or workspace.root")
	}
	loader := workspacectx.NewLoader(app.Config.Workspace.Root)
	result, err := loader.ResolveDurableReview(ctx, workspacectx.DurableWriteInput{
		ProfileID:        payload.Scope.ProfileID,
		PersonID:         payload.Scope.PersonID,
		RoomID:           payload.Scope.RoomID,
		ConversationID:   payload.Scope.ConversationID,
		ConversationSlug: payload.ConversationSlug,
		Scope:            payload.Scope.Kind,
		Facts:            append([]string(nil), payload.Facts...),
		UpdatedBy:        payload.UpdatedBy,
		Timestamp:        job.CreatedAt,
	}, payload.Conflicts)
	if err != nil {
		return err
	}
	if err := app.Repos.MemoryJobs.UpdateStatus(ctx, job.ID, domain.MemoryJobDone, "approved"); err != nil {
		return err
	}
	return writeJSON(stdout, map[string]any{
		"job_id":        job.ID,
		"status":        string(domain.MemoryJobDone),
		"path":          result.Path,
		"added_facts":   result.AddedFacts,
		"skipped_facts": result.SkippedFacts,
		"removed_facts": result.RemovedFacts,
	})
}

func runMemoryReviewReject(ctx context.Context, app *runtime.App, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("memory review reject", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var jobID string
	var reason string
	fs.StringVar(&jobID, "job-id", "", "review job id")
	fs.StringVar(&reason, "reason", "rejected", "rejection reason")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(jobID) == "" {
		return errors.New("missing required flag --job-id")
	}

	job, _, err := loadReviewJob(ctx, app, jobID)
	if err != nil {
		return err
	}
	if err := app.Repos.MemoryJobs.UpdateStatus(ctx, job.ID, domain.MemoryJobRejected, strings.TrimSpace(reason)); err != nil {
		return err
	}
	return writeJSON(stdout, map[string]any{
		"job_id": job.ID,
		"status": string(domain.MemoryJobRejected),
		"reason": strings.TrimSpace(reason),
	})
}

func loadReviewJob(
	ctx context.Context,
	app *runtime.App,
	jobID string,
) (domain.MemoryJob, runtime.DurableReviewJobPayload, error) {
	job, err := app.Repos.MemoryJobs.Get(ctx, strings.TrimSpace(jobID))
	if err != nil {
		return domain.MemoryJob{}, runtime.DurableReviewJobPayload{}, err
	}
	if job.Kind != runtime.MemoryJobKindMarkdownReview {
		return domain.MemoryJob{}, runtime.DurableReviewJobPayload{}, fmt.Errorf("memory job %q is not a review job", job.ID)
	}
	if job.Status != domain.MemoryJobReview {
		return domain.MemoryJob{}, runtime.DurableReviewJobPayload{}, fmt.Errorf("memory job %q is not pending review", job.ID)
	}
	var payload runtime.DurableReviewJobPayload
	if err := json.Unmarshal([]byte(job.PayloadJSON), &payload); err != nil {
		return domain.MemoryJob{}, runtime.DurableReviewJobPayload{}, fmt.Errorf("decode review payload %s: %w", job.ID, err)
	}
	return job, payload, nil
}

func parseMemoryPromoteScope(value string) (memory.ScopeKind, error) {
	switch memory.ScopeKind(strings.TrimSpace(value)) {
	case memory.ScopeConversation, memory.ScopeRoom, memory.ScopePersonSummary, memory.ScopePersonPrivate:
		return memory.ScopeKind(strings.TrimSpace(value)), nil
	default:
		return "", fmt.Errorf(
			"invalid --scope %q (expected conversation, room, person_summary, or person_private)",
			value,
		)
	}
}

func ensureMemoryPromoteTargetExists(
	ctx context.Context,
	app *runtime.App,
	scope memory.ScopeKind,
	profileID string,
	roomID string,
	personID string,
	slug string,
) error {
	switch scope {
	case memory.ScopeConversation:
		if err := requireConversationTarget(profileID, roomID, slug); err != nil {
			return err
		}
		if err := ensureConversationRoomExists(ctx, app, profileID, roomID); err != nil {
			return err
		}
		_, err := app.Repos.Conversations.GetByProfileRoomSlug(
			ctx,
			domain.ProfileID(strings.TrimSpace(profileID)),
			domain.RoomID(strings.TrimSpace(roomID)),
			strings.TrimSpace(slug),
		)
		return err
	case memory.ScopeRoom:
		if err := requirePermissionTarget(profileID, roomID); err != nil {
			return err
		}
		return ensurePermissionTargetExists(ctx, app, profileID, roomID)
	case memory.ScopePersonSummary, memory.ScopePersonPrivate:
		if strings.TrimSpace(profileID) == "" {
			return errors.New("missing required flag --profile-id")
		}
		if strings.TrimSpace(personID) == "" {
			return errors.New("missing required flag --person-id")
		}
		if _, err := app.Repos.Profiles.Get(ctx, domain.ProfileID(strings.TrimSpace(profileID))); err != nil {
			return err
		}
		if _, err := app.Repos.Persons.Get(ctx, domain.PersonID(strings.TrimSpace(personID))); err != nil {
			return err
		}
		return nil
	default:
		return fmt.Errorf("unsupported memory scope %q", scope)
	}
}

func resolveMemoryPromoteSourceMessage(
	ctx context.Context,
	app *runtime.App,
	scope memory.ScopeKind,
	profileID string,
	roomID string,
	personID string,
	slug string,
) (domain.Message, error) {
	switch scope {
	case memory.ScopeConversation:
		conversation, err := app.Repos.Conversations.GetByProfileRoomSlug(
			ctx,
			domain.ProfileID(strings.TrimSpace(profileID)),
			domain.RoomID(strings.TrimSpace(roomID)),
			strings.TrimSpace(slug),
		)
		if err != nil {
			return domain.Message{}, err
		}
		messages, err := app.Repos.Messages.ListRecentByConversation(ctx, conversation.ID, 1)
		if err != nil {
			return domain.Message{}, err
		}
		if len(messages) == 0 {
			return domain.Message{}, errors.New("cannot queue conversation promotion without at least one conversation message")
		}
		return messages[0], nil
	case memory.ScopeRoom:
		messages, err := app.Repos.Messages.ListRecentByRoom(ctx, domain.RoomID(strings.TrimSpace(roomID)), 1)
		if err != nil {
			return domain.Message{}, err
		}
		if len(messages) == 0 {
			return domain.Message{}, errors.New("cannot queue room promotion without at least one room message")
		}
		return messages[0], nil
	case memory.ScopePersonSummary, memory.ScopePersonPrivate:
		messages, err := app.Repos.Messages.ListRecentByPerson(ctx, domain.PersonID(strings.TrimSpace(personID)), 1)
		if err != nil {
			return domain.Message{}, err
		}
		if len(messages) == 0 {
			return domain.Message{}, errors.New("cannot queue person promotion without at least one person message")
		}
		return messages[0], nil
	default:
		return domain.Message{}, fmt.Errorf("unsupported memory scope %q", scope)
	}
}
