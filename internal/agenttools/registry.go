package agenttools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"goclaw/internal/domain"
	"goclaw/internal/model"
	sqlitestore "goclaw/internal/store/sqlite"
)

type Registry struct {
	providers []Provider
}

func NewRegistry(providers ...Provider) *Registry {
	filtered := make([]Provider, 0, len(providers))
	for _, provider := range providers {
		if provider == nil {
			continue
		}
		filtered = append(filtered, provider)
	}
	return &Registry{providers: filtered}
}

func (r *Registry) VisibleDefinitions(
	session SessionContext,
	policy domain.ToolPermissionPolicy,
) ([]Definition, error) {
	if r == nil {
		return nil, nil
	}

	out := make([]Definition, 0)
	seen := make(map[string]struct{})
	for _, provider := range r.providers {
		for _, definition := range provider.VisibleTools(session, policy) {
			name := strings.TrimSpace(definition.Name)
			if name == "" {
				return nil, fmt.Errorf("agenttools: encountered tool with empty name")
			}
			if _, ok := seen[name]; ok {
				return nil, fmt.Errorf("agenttools: duplicate tool name %q", name)
			}
			seen[name] = struct{}{}
			definition.Name = name
			out = append(out, definition)
		}
	}
	slices.SortFunc(out, func(a, b Definition) int {
		return strings.Compare(a.Name, b.Name)
	})
	return out, nil
}

func (r *Registry) ChannelCapabilitySummary(
	ctx context.Context,
	session SessionContext,
	policy domain.ToolPermissionPolicy,
) (CapabilitySummary, error) {
	if r == nil {
		return CapabilitySummary{}, nil
	}

	for _, provider := range r.providers {
		summary, handled, err := provider.CapabilitySummary(ctx, session, policy)
		if err != nil {
			return CapabilitySummary{}, err
		}
		if handled {
			return summary, nil
		}
	}
	return CapabilitySummary{}, nil
}

func (r *Registry) VisibleModelTools(
	session SessionContext,
	policy domain.ToolPermissionPolicy,
) ([]model.Tool, []Definition, error) {
	definitions, err := r.VisibleDefinitions(session, policy)
	if err != nil {
		return nil, nil, err
	}
	tools := make([]model.Tool, 0, len(definitions))
	for _, definition := range definitions {
		tools = append(tools, definition.ModelTool())
	}
	return tools, definitions, nil
}

func (r *Registry) LookupVisibleDefinition(
	session SessionContext,
	policy domain.ToolPermissionPolicy,
	name string,
) (Definition, bool, error) {
	definitions, err := r.VisibleDefinitions(session, policy)
	if err != nil {
		return Definition{}, false, err
	}
	normalized := strings.TrimSpace(name)
	for _, definition := range definitions {
		if definition.Name == normalized {
			return definition, true, nil
		}
	}
	return Definition{}, false, nil
}

func (r *Registry) Execute(
	ctx context.Context,
	session SessionContext,
	policy domain.ToolPermissionPolicy,
	logger *slog.Logger,
	repos sqlitestore.Repositories,
	now func() time.Time,
	name string,
	args json.RawMessage,
) (string, Definition, error) {
	if r == nil {
		return "", Definition{}, fmt.Errorf("agenttools: registry is not configured")
	}

	definition, ok, err := r.LookupVisibleDefinition(session, policy, name)
	if err != nil {
		return "", Definition{}, err
	}
	if !ok {
		content, marshalErr := MarshalError(strings.TrimSpace(name), &ExecutionError{
			Kind:    ErrorPermissionDenied,
			Tool:    strings.TrimSpace(name),
			Message: "tool is not visible in the current room policy",
		})
		return content, Definition{}, marshalErr
	}

	execCtx := ExecutionContext{
		Session:          session,
		Account:          definition.Account,
		ProviderMetadata: definition.ProviderMetadata,
		Logger:           logger,
		Repos:            repos,
		Now:              now,
	}
	if execCtx.Now == nil {
		execCtx.Now = time.Now
	}

	result, execErr := definition.Execute(ctx, execCtx, append(json.RawMessage(nil), args...))
	if execErr != nil {
		content, marshalErr := MarshalError(definition.Name, execErr)
		return content, definition, marshalErr
	}
	content, marshalErr := MarshalSuccess(definition.Name, result)
	return content, definition, marshalErr
}
