package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"goclaw/internal/domain"
)

type ReplyRunsRepository struct{ db DBTX }

func (r ReplyRunsRepository) Insert(ctx context.Context, run domain.ReplyRun) error {
	_, err := r.db.ExecContext(
		ctx,
		`INSERT INTO agent_reply_runs(
		   id, profile_id, room_id, conversation_id, session_id, inbound_message_id,
		   model_provider, tooling_mode, status, error_text, started_at, finished_at
		 )
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);`,
		run.ID,
		string(run.ProfileID),
		string(run.RoomID),
		string(run.ConversationID),
		string(run.SessionID),
		string(run.InboundMessageID),
		run.ModelProvider,
		run.ToolingMode,
		string(run.Status),
		run.ErrorText,
		formatTime(run.StartedAt),
		nullableTime(run.FinishedAt),
	)
	if err != nil {
		return fmt.Errorf("insert reply run %s: %w", run.ID, err)
	}
	return nil
}

func (r ReplyRunsRepository) Get(ctx context.Context, id string) (domain.ReplyRun, error) {
	return scanReplyRun(
		r.db.QueryRowContext(
			ctx,
			`SELECT id, profile_id, room_id, conversation_id, session_id, inbound_message_id,
			        model_provider, tooling_mode, status, error_text, started_at, finished_at
			 FROM agent_reply_runs
			 WHERE id = ?;`,
			id,
		),
	)
}

func (r ReplyRunsRepository) ListRecentByRoom(
	ctx context.Context,
	profileID domain.ProfileID,
	roomID domain.RoomID,
	limit int,
) ([]domain.ReplyRun, error) {
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT id, profile_id, room_id, conversation_id, session_id, inbound_message_id,
		        model_provider, tooling_mode, status, error_text, started_at, finished_at
		 FROM agent_reply_runs
		 WHERE profile_id = ? AND room_id = ?
		 ORDER BY started_at DESC, id DESC
		 LIMIT ?;`,
		string(profileID),
		string(roomID),
		clampLimit(limit),
	)
	if err != nil {
		return nil, fmt.Errorf("list reply runs for room %s/%s: %w", profileID, roomID, err)
	}
	return scanReplyRuns(rows, "reply runs")
}

func (r ReplyRunsRepository) Finish(
	ctx context.Context,
	id string,
	status domain.ReplyRunStatus,
	errorText string,
	finishedAt time.Time,
) error {
	result, err := r.db.ExecContext(
		ctx,
		`UPDATE agent_reply_runs
		 SET status = ?, error_text = ?, finished_at = ?
		 WHERE id = ?;`,
		string(status),
		errorText,
		formatTime(finishedAt),
		id,
	)
	if err != nil {
		return fmt.Errorf("finish reply run %s: %w", id, err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("finish reply run %s rows affected: %w", id, err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("reply run: %w", ErrNotFound)
	}
	return nil
}

type ToolInvocationsRepository struct{ db DBTX }

func (r ToolInvocationsRepository) Insert(ctx context.Context, invocation domain.ToolInvocation) error {
	_, err := r.db.ExecContext(
		ctx,
		`INSERT INTO tool_invocations(
		   id, reply_run_id, iteration, tool_call_id, tool_name, tool_source, provider, capability_id, args_json, decision_json,
		   status, result_json, error_text, started_at, finished_at
		 )
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);`,
		invocation.ID,
		invocation.ReplyRunID,
		invocation.Iteration,
		invocation.ToolCallID,
		invocation.ToolName,
		invocation.ToolSource,
		string(invocation.Provider),
		invocation.CapabilityID,
		normalizeJSONObject(invocation.ArgsJSON),
		normalizeJSONObject(invocation.DecisionJSON),
		string(invocation.Status),
		normalizeJSONObject(invocation.ResultJSON),
		invocation.ErrorText,
		formatTime(invocation.StartedAt),
		nullableTime(invocation.FinishedAt),
	)
	if err != nil {
		return fmt.Errorf("insert tool invocation %s: %w", invocation.ID, err)
	}
	return nil
}

func (r ToolInvocationsRepository) Get(ctx context.Context, id string) (domain.ToolInvocation, error) {
	return scanToolInvocation(
		r.db.QueryRowContext(
			ctx,
			`SELECT id, reply_run_id, iteration, tool_call_id, tool_name, tool_source, provider, capability_id, args_json, decision_json,
			        status, result_json, error_text, started_at, finished_at
			 FROM tool_invocations
			 WHERE id = ?;`,
			id,
		),
	)
}

func (r ToolInvocationsRepository) ListByReplyRun(
	ctx context.Context,
	replyRunID string,
) ([]domain.ToolInvocation, error) {
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT id, reply_run_id, iteration, tool_call_id, tool_name, tool_source, provider, capability_id, args_json, decision_json,
		        status, result_json, error_text, started_at, finished_at
		 FROM tool_invocations
		 WHERE reply_run_id = ?
		 ORDER BY iteration ASC, started_at ASC, id ASC;`,
		replyRunID,
	)
	if err != nil {
		return nil, fmt.Errorf("list tool invocations for reply run %s: %w", replyRunID, err)
	}
	return scanToolInvocations(rows, "tool invocations")
}

func (r ToolInvocationsRepository) Finish(
	ctx context.Context,
	id string,
	status domain.ToolInvocationStatus,
	resultJSON string,
	errorText string,
	finishedAt time.Time,
) error {
	result, err := r.db.ExecContext(
		ctx,
		`UPDATE tool_invocations
		 SET status = ?, result_json = ?, error_text = ?, finished_at = ?
		 WHERE id = ?;`,
		string(status),
		normalizeJSONObject(resultJSON),
		errorText,
		formatTime(finishedAt),
		id,
	)
	if err != nil {
		return fmt.Errorf("finish tool invocation %s: %w", id, err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("finish tool invocation %s rows affected: %w", id, err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("tool invocation: %w", ErrNotFound)
	}
	return nil
}

func scanReplyRun(row *sql.Row) (domain.ReplyRun, error) {
	var (
		run            domain.ReplyRun
		startedAtText  string
		finishedAtText sql.NullString
	)
	err := row.Scan(
		&run.ID,
		&run.ProfileID,
		&run.RoomID,
		&run.ConversationID,
		&run.SessionID,
		&run.InboundMessageID,
		&run.ModelProvider,
		&run.ToolingMode,
		&run.Status,
		&run.ErrorText,
		&startedAtText,
		&finishedAtText,
	)
	if err != nil {
		return domain.ReplyRun{}, wrapNotFound("reply run", err)
	}
	parsedStartedAt, err := parseTime(startedAtText)
	if err != nil {
		return domain.ReplyRun{}, fmt.Errorf("parse reply run started_at: %w", err)
	}
	run.StartedAt = parsedStartedAt
	if finishedAtText.Valid {
		parsedFinishedAt, err := parseTime(finishedAtText.String)
		if err != nil {
			return domain.ReplyRun{}, fmt.Errorf("parse reply run finished_at: %w", err)
		}
		run.FinishedAt = &parsedFinishedAt
	}
	return run, nil
}

func scanReplyRuns(rows *sql.Rows, contextLabel string) ([]domain.ReplyRun, error) {
	defer rows.Close()

	var runs []domain.ReplyRun
	for rows.Next() {
		var (
			run            domain.ReplyRun
			startedAtText  string
			finishedAtText sql.NullString
		)
		if err := rows.Scan(
			&run.ID,
			&run.ProfileID,
			&run.RoomID,
			&run.ConversationID,
			&run.SessionID,
			&run.InboundMessageID,
			&run.ModelProvider,
			&run.ToolingMode,
			&run.Status,
			&run.ErrorText,
			&startedAtText,
			&finishedAtText,
		); err != nil {
			return nil, fmt.Errorf("scan %s row: %w", contextLabel, err)
		}
		parsedStartedAt, err := parseTime(startedAtText)
		if err != nil {
			return nil, fmt.Errorf("parse %s started_at: %w", contextLabel, err)
		}
		run.StartedAt = parsedStartedAt
		if finishedAtText.Valid {
			parsedFinishedAt, err := parseTime(finishedAtText.String)
			if err != nil {
				return nil, fmt.Errorf("parse %s finished_at: %w", contextLabel, err)
			}
			run.FinishedAt = &parsedFinishedAt
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate %s: %w", contextLabel, err)
	}
	return runs, nil
}

func scanToolInvocation(row *sql.Row) (domain.ToolInvocation, error) {
	var (
		invocation     domain.ToolInvocation
		startedAtText  string
		finishedAtText sql.NullString
	)
	err := row.Scan(
		&invocation.ID,
		&invocation.ReplyRunID,
		&invocation.Iteration,
		&invocation.ToolCallID,
		&invocation.ToolName,
		&invocation.ToolSource,
		&invocation.Provider,
		&invocation.CapabilityID,
		&invocation.ArgsJSON,
		&invocation.DecisionJSON,
		&invocation.Status,
		&invocation.ResultJSON,
		&invocation.ErrorText,
		&startedAtText,
		&finishedAtText,
	)
	if err != nil {
		return domain.ToolInvocation{}, wrapNotFound("tool invocation", err)
	}
	parsedStartedAt, err := parseTime(startedAtText)
	if err != nil {
		return domain.ToolInvocation{}, fmt.Errorf("parse tool invocation started_at: %w", err)
	}
	invocation.StartedAt = parsedStartedAt
	if finishedAtText.Valid {
		parsedFinishedAt, err := parseTime(finishedAtText.String)
		if err != nil {
			return domain.ToolInvocation{}, fmt.Errorf("parse tool invocation finished_at: %w", err)
		}
		invocation.FinishedAt = &parsedFinishedAt
	}
	return invocation, nil
}

func scanToolInvocations(rows *sql.Rows, contextLabel string) ([]domain.ToolInvocation, error) {
	defer rows.Close()

	var invocations []domain.ToolInvocation
	for rows.Next() {
		var (
			invocation     domain.ToolInvocation
			startedAtText  string
			finishedAtText sql.NullString
		)
		if err := rows.Scan(
			&invocation.ID,
			&invocation.ReplyRunID,
			&invocation.Iteration,
			&invocation.ToolCallID,
			&invocation.ToolName,
			&invocation.ToolSource,
			&invocation.Provider,
			&invocation.CapabilityID,
			&invocation.ArgsJSON,
			&invocation.DecisionJSON,
			&invocation.Status,
			&invocation.ResultJSON,
			&invocation.ErrorText,
			&startedAtText,
			&finishedAtText,
		); err != nil {
			return nil, fmt.Errorf("scan %s row: %w", contextLabel, err)
		}
		parsedStartedAt, err := parseTime(startedAtText)
		if err != nil {
			return nil, fmt.Errorf("parse %s started_at: %w", contextLabel, err)
		}
		invocation.StartedAt = parsedStartedAt
		if finishedAtText.Valid {
			parsedFinishedAt, err := parseTime(finishedAtText.String)
			if err != nil {
				return nil, fmt.Errorf("parse %s finished_at: %w", contextLabel, err)
			}
			invocation.FinishedAt = &parsedFinishedAt
		}
		invocations = append(invocations, invocation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate %s: %w", contextLabel, err)
	}
	return invocations, nil
}
