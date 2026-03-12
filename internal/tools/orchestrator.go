package tools

import (
	"context"
	"fmt"

	"goclaw/internal/agenttools"
	"goclaw/internal/domain"
	"goclaw/internal/model"
)

const defaultMaxIterations = 3

type SystemPromptAppender func(string, domain.ToolPermissionPolicy) string

type ExecutionObserver func(iteration int, call ToolCallBlock, native bool)

type RunStartHook func(context.Context) error

type RunFinishHook func(context.Context, string, error) error

type InvocationStartHook func(context.Context, int, ToolCallBlock, bool) (string, error)

type InvocationFinishHook func(context.Context, string, string, error) error

type Orchestrator struct {
	Registry           *agenttools.Registry
	Session            agenttools.SessionContext
	Execution          agenttools.ExecutionContext
	Policy             domain.ToolPermissionPolicy
	MaxIterations      int
	AppendLegacyPrompt SystemPromptAppender
	AppendNativePrompt SystemPromptAppender
	ObserveExecution   ExecutionObserver
	OnRunStart         RunStartHook
	OnRunFinish        RunFinishHook
	OnInvocationStart  InvocationStartHook
	OnInvocationFinish InvocationFinishHook
}

func (o Orchestrator) Run(
	ctx context.Context,
	provider model.Provider,
	request model.Request,
) (finalText string, runErr error) {
	if o.OnRunStart != nil {
		if err := o.OnRunStart(ctx); err != nil {
			return "", err
		}
	}
	defer func() {
		if o.OnRunFinish == nil {
			return
		}
		if finishErr := o.OnRunFinish(ctx, finalText, runErr); finishErr != nil && runErr == nil {
			finalText = ""
			runErr = finishErr
		}
	}()
	if native, ok := model.NativeToolProviderFor(provider); ok {
		return o.runNative(ctx, provider, native, request)
	}
	return o.runLegacy(ctx, provider, request)
}

func (o Orchestrator) runNative(
	ctx context.Context,
	provider model.Provider,
	native model.NativeToolProvider,
	request model.Request,
) (string, error) {
	if o.Registry == nil {
		return provider.Generate(ctx, request)
	}
	toolDefinitions, _, err := o.Registry.VisibleModelTools(o.Session, o.Policy)
	if err != nil {
		return "", err
	}
	if len(toolDefinitions) == 0 {
		return provider.Generate(ctx, request)
	}
	if o.AppendNativePrompt != nil {
		request.System = o.AppendNativePrompt(request.System, o.Policy)
	}
	iteration := 0
	return native.GenerateWithTools(ctx, request, model.ToolConfig{
		Tools:         toolDefinitions,
		MaxIterations: o.maxIterations(),
	}, func(callCtx context.Context, call model.ToolCall) (model.ToolResult, error) {
		iteration++
		return o.executeNativeToolCall(callCtx, call, iteration)
	})
}

func (o Orchestrator) runLegacy(
	ctx context.Context,
	provider model.Provider,
	request model.Request,
) (string, error) {
	if o.Registry == nil {
		return provider.Generate(ctx, request)
	}
	toolDefinitions, _, err := o.Registry.VisibleModelTools(o.Session, o.Policy)
	if err != nil {
		return "", err
	}
	if len(toolDefinitions) == 0 {
		return provider.Generate(ctx, request)
	}
	if o.AppendLegacyPrompt != nil {
		request.System = o.AppendLegacyPrompt(request.System, o.Policy)
	}
	for iteration := 1; iteration <= o.maxIterations(); iteration++ {
		output, err := provider.Generate(ctx, request)
		if err != nil {
			return "", err
		}

		turn := ParseLegacyOutput(output)
		toolCalls := turn.ToolCalls()
		if len(toolCalls) == 0 {
			return turn.Text(), nil
		}

		for _, call := range toolCalls {
			toolResult, err := o.executeToolCall(ctx, call, iteration, false)
			if err != nil {
				return "", err
			}
			request.Messages = append(request.Messages,
				model.Message{
					Role:    string(domain.MessageRoleAssistant),
					Content: ToolCallSummary(call),
				},
				model.Message{
					Role: "user",
					Content: fmt.Sprintf(
						"[Tool Result]\n%s\n\nIf you need another tool, emit another JSON tool request. Otherwise answer the user normally.",
						toolResult,
					),
				},
			)
		}
	}
	return "", fmt.Errorf("runtime: tool loop exceeded %d iterations", o.maxIterations())
}

func (o Orchestrator) executeNativeToolCall(
	ctx context.Context,
	call model.ToolCall,
	iteration int,
) (model.ToolResult, error) {
	normalized := NormalizeAssistantTurn(AssistantTurn{
		StopReason: StopReasonToolUse,
		Blocks: []AssistantBlock{
			ToolCallBlock{
				ID:   call.ID,
				Name: call.Name,
				Args: append([]byte(nil), call.Input...),
			},
		},
	})
	toolCalls := normalized.ToolCalls()
	if len(toolCalls) == 0 {
		return model.ToolResult{}, fmt.Errorf("runtime: native tool call is empty")
	}

	content, err := o.executeToolCall(ctx, toolCalls[0], iteration, true)
	if err != nil {
		return model.ToolResult{}, err
	}
	return model.ToolResult{
		ToolCallID: toolCalls[0].ID,
		ToolName:   toolCalls[0].Name,
		Content:    content,
		IsError:    ResultIsError(content),
	}, nil
}

func (o Orchestrator) executeToolCall(
	ctx context.Context,
	call ToolCallBlock,
	iteration int,
	native bool,
) (string, error) {
	invocationID := ""
	if o.OnInvocationStart != nil {
		var err error
		invocationID, err = o.OnInvocationStart(ctx, iteration, call, native)
		if err != nil {
			return "", err
		}
	}
	content, _, err := o.Registry.Execute(
		ctx,
		o.Session,
		o.Policy,
		o.Execution.Logger,
		o.Execution.Repos,
		o.Execution.Now,
		call.Name,
		call.Args,
	)
	if o.OnInvocationFinish != nil {
		finishErr := o.OnInvocationFinish(ctx, invocationID, content, err)
		if err == nil && finishErr != nil {
			return "", finishErr
		}
	}
	if err != nil {
		return "", err
	}
	if o.ObserveExecution != nil {
		o.ObserveExecution(iteration, call, native)
	}
	return content, nil
}

func (o Orchestrator) maxIterations() int {
	if o.MaxIterations > 0 {
		return o.MaxIterations
	}
	return defaultMaxIterations
}
