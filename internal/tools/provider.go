package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"goclaw/internal/agenttools"
	"goclaw/internal/domain"
)

type localToolProvider struct {
	runtime *LocalRuntime
}

func NewLocalToolProvider(runtime *LocalRuntime) agenttools.Provider {
	return &localToolProvider{runtime: runtime}
}

func (p *localToolProvider) VisibleTools(
	_ agenttools.SessionContext,
	policy domain.ToolPermissionPolicy,
) []agenttools.Definition {
	localDefinitions := VisibleDefinitions(policy)
	out := make([]agenttools.Definition, 0, len(localDefinitions))
	for _, definition := range localDefinitions {
		localDefinition := definition
		out = append(out, agenttools.Definition{
			Name:               localDefinition.Name,
			Description:        localDefinition.Description,
			InputSchema:        append(json.RawMessage(nil), localDefinition.InputSchema...),
			Source:             agenttools.SourceLocal,
			ProfileBindingMode: agenttools.ProfileBindingCurrentSession,
			SafetyClass:        localToolSafetyClass(localDefinition.Name),
			CapabilityID:       localToolCapabilityID(localDefinition.Name),
			Execute: func(
				ctx context.Context,
				execCtx agenttools.ExecutionContext,
				raw json.RawMessage,
			) (any, error) {
				if p.runtime == nil {
					return nil, &agenttools.ExecutionError{
						Kind:    agenttools.ErrorProvider,
						Tool:    localDefinition.Name,
						Message: "local tool runtime is not configured",
					}
				}
				result, err := localDefinition.Execute(
					ctx,
					p.runtime,
					execCtx.Session.ProfileID,
					execCtx.Session.RoomID,
					append(json.RawMessage(nil), raw...),
				)
				if err == nil {
					return result, nil
				}

				var denied PermissionDeniedError
				if errors.As(err, &denied) {
					return nil, &agenttools.ExecutionError{
						Kind:    agenttools.ErrorPermissionDenied,
						Tool:    localDefinition.Name,
						Message: err.Error(),
						Details: map[string]any{
							"reason":       denied.Decision.Reason,
							"matched_rule": denied.Decision.MatchedRule,
						},
					}
				}
				return nil, &agenttools.ExecutionError{
					Kind:    agenttools.ErrorProvider,
					Tool:    localDefinition.Name,
					Message: err.Error(),
				}
			},
		})
	}
	return out
}

func (p *localToolProvider) CapabilitySummary(
	_ context.Context,
	_ agenttools.SessionContext,
	_ domain.ToolPermissionPolicy,
) (agenttools.CapabilitySummary, bool, error) {
	return agenttools.CapabilitySummary{}, false, nil
}

func localToolSafetyClass(name string) agenttools.SafetyClass {
	switch strings.TrimSpace(name) {
	case "exec":
		return agenttools.SafetySensitive
	case "write":
		return agenttools.SafetyMutating
	default:
		return agenttools.SafetyReadOnly
	}
}

func localToolCapabilityID(name string) string {
	switch strings.TrimSpace(name) {
	case "exec":
		return "local.exec"
	case "read":
		return "local.fs.read"
	case "write":
		return "local.fs.write"
	case "fetch":
		return "local.network.fetch"
	default:
		return "local." + strings.TrimSpace(name)
	}
}
