package openaicompat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"goclaw/internal/model"
)

type chatCompletionsMessage struct {
	Role       string                    `json:"role"`
	Content    any                       `json:"content"`
	ToolCalls  []chatCompletionsToolCall `json:"tool_calls,omitempty"`
	ToolCallID string                    `json:"tool_call_id,omitempty"`
}

type chatCompletionsToolCall struct {
	ID       string                          `json:"id,omitempty"`
	Type     string                          `json:"type"`
	Function chatCompletionsToolCallFunction `json:"function"`
}

type chatCompletionsToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type chatCompletionsResponse struct {
	Choices []chatCompletionsChoice `json:"choices"`
}

type chatCompletionsChoice struct {
	Message      chatCompletionsChoiceMessage `json:"message"`
	FinishReason string                       `json:"finish_reason,omitempty"`
}

type chatCompletionsChoiceMessage struct {
	Content   any                       `json:"content"`
	ToolCalls []chatCompletionsToolCall `json:"tool_calls,omitempty"`
}

const chatCompletionsBootstrapText = "(session bootstrap)"
const chatCompletionsExplorationHint = "[Tooling Notice] Use the tool results already gathered to answer if they are sufficient. If more tool work is still required, batch independent tool calls into a single assistant turn instead of exploring one path at a time."

func (p *Provider) generateWithChatCompletionTools(
	ctx context.Context,
	request model.Request,
	toolConfig model.ToolConfig,
	executor model.ToolExecutor,
) (string, error) {
	messages := toChatCompletionMessages(request)
	maxIterations := toolConfig.MaxIterations
	if maxIterations <= 0 {
		maxIterations = defaultToolIterations
	}
	tools := toResponsesTools(toolConfig.Tools)
	repeatedToolStreak := 0
	var previousRound []model.ToolCall

	for iteration := 0; iteration < maxIterations; iteration++ {
		response, err := p.createChatCompletionToolResponse(ctx, messages, tools)
		if err != nil {
			return "", err
		}

		toolCalls, err := response.toolCalls()
		if err != nil {
			return "", err
		}
		if len(toolCalls) == 0 {
			return response.text(), nil
		}
		repeatedToolStreak = nextRepeatedToolStreak(previousRound, toolCalls, repeatedToolStreak)

		replayMessage, ok := response.replayMessage()
		if ok {
			messages = append(messages, replayMessage)
		}
		toolMessages, err := executeChatCompletionToolCalls(ctx, toolCalls, executor)
		if err != nil {
			return "", err
		}
		messages = append(messages, toolMessages...)
		if shouldAppendChatCompletionExplorationHint(repeatedToolStreak, toolCalls, messages) {
			messages = append(messages, chatCompletionsMessage{
				Role:    "user",
				Content: chatCompletionsExplorationHint,
			})
		}
		previousRound = cloneToolCalls(toolCalls)
	}

	return "", fmt.Errorf("openai chat completions tool loop exceeded %d iterations", maxIterations)
}

func shouldAppendChatCompletionExplorationHint(
	repeatedToolStreak int,
	toolCalls []model.ToolCall,
	messages []chatCompletionsMessage,
) bool {
	if repeatedToolStreak < 1 || len(toolCalls) == 0 {
		return false
	}
	if last := lastChatCompletionConversationMessage(messages); normalizeChatCompletionRole(last.Role) == "user" {
		if text, ok := last.Content.(string); ok && strings.TrimSpace(text) == chatCompletionsExplorationHint {
			return false
		}
	}
	if allExecDirectoryInspection(toolCalls) {
		return true
	}
	return allSameToolFamily(toolCalls)
}

func lastChatCompletionConversationMessage(messages []chatCompletionsMessage) chatCompletionsMessage {
	for index := len(messages) - 1; index >= 0; index-- {
		if normalizeChatCompletionRole(messages[index].Role) == "system" {
			continue
		}
		return messages[index]
	}
	return chatCompletionsMessage{}
}

func nextRepeatedToolStreak(previousRound, currentRound []model.ToolCall, currentStreak int) int {
	if !sameToolFamilies(previousRound, currentRound) {
		return 0
	}
	return currentStreak + 1
}

func sameToolFamilies(left, right []model.ToolCall) bool {
	if len(left) == 0 || len(right) == 0 {
		return false
	}
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if strings.TrimSpace(left[index].Name) != strings.TrimSpace(right[index].Name) {
			return false
		}
	}
	return true
}

func allSameToolFamily(toolCalls []model.ToolCall) bool {
	if len(toolCalls) == 0 {
		return false
	}
	name := strings.TrimSpace(toolCalls[0].Name)
	if name == "" {
		return false
	}
	for _, call := range toolCalls[1:] {
		if strings.TrimSpace(call.Name) != name {
			return false
		}
	}
	return true
}

func cloneToolCalls(calls []model.ToolCall) []model.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]model.ToolCall, 0, len(calls))
	for _, call := range calls {
		out = append(out, model.ToolCall{
			ID:    call.ID,
			Name:  call.Name,
			Input: append(json.RawMessage(nil), call.Input...),
		})
	}
	return out
}

func allExecDirectoryInspection(toolCalls []model.ToolCall) bool {
	if len(toolCalls) == 0 {
		return false
	}
	for _, call := range toolCalls {
		if strings.TrimSpace(call.Name) != "exec" {
			return false
		}
		var input struct {
			Program string   `json:"program"`
			Args    []string `json:"args"`
		}
		if err := json.Unmarshal(call.Input, &input); err != nil {
			return false
		}
		if strings.TrimSpace(input.Program) != "ls" {
			return false
		}
	}
	return true
}

func (p *Provider) createChatCompletionToolResponse(
	ctx context.Context,
	messages []chatCompletionsMessage,
	tools []responsesFunctionTool,
) (chatCompletionsResponse, error) {
	sanitizedMessages := repairChatCompletionMessages(messages)
	payload := struct {
		Model             string                   `json:"model"`
		Messages          []chatCompletionsMessage `json:"messages"`
		Tools             []responsesFunctionTool  `json:"tools,omitempty"`
		ToolChoice        string                   `json:"tool_choice,omitempty"`
		ParallelToolCalls *bool                    `json:"parallel_tool_calls,omitempty"`
	}{
		Model:    p.modelID,
		Messages: sanitizedMessages,
		Tools:    tools,
	}
	if len(tools) > 0 {
		payload.ToolChoice = "auto"
		parallelToolCalls := true
		payload.ParallelToolCalls = &parallelToolCalls
	}

	responseBody, err := p.postJSON(ctx, "/chat/completions", payload)
	if err != nil {
		return chatCompletionsResponse{}, err
	}

	var response chatCompletionsResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return chatCompletionsResponse{}, err
	}
	return response, nil
}

func toChatCompletionMessages(request model.Request) []chatCompletionsMessage {
	messages := make([]chatCompletionsMessage, 0, len(request.Messages)+1)
	if system := strings.TrimSpace(request.System); system != "" {
		messages = append(messages, chatCompletionsMessage{
			Role:    "system",
			Content: system,
		})
	}
	for _, message := range request.Messages {
		content := strings.TrimSpace(message.Content)
		if content == "" {
			continue
		}
		messages = append(messages, chatCompletionsMessage{
			Role:    normalizeChatCompletionRole(message.Role),
			Content: content,
		})
	}
	return repairChatCompletionMessages(messages)
}

func normalizeChatCompletionRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "assistant":
		return "assistant"
	case "system":
		return "system"
	case "tool":
		return "tool"
	default:
		return "user"
	}
}

func mergeAdjacentChatCompletionTextMessages(messages []chatCompletionsMessage) []chatCompletionsMessage {
	if len(messages) == 0 {
		return nil
	}
	out := make([]chatCompletionsMessage, 0, len(messages))
	for _, message := range messages {
		normalized := normalizeChatCompletionMessage(message)
		if chatCompletionMessageEmpty(normalized) {
			continue
		}
		if len(out) == 0 {
			out = append(out, normalized)
			continue
		}
		lastIndex := len(out) - 1
		if merged, ok := mergeChatCompletionTextPair(out[lastIndex], normalized); ok {
			out[lastIndex] = merged
			continue
		}
		out = append(out, normalized)
	}
	return out
}

func normalizeChatCompletionMessage(message chatCompletionsMessage) chatCompletionsMessage {
	message.Role = normalizeChatCompletionRole(message.Role)
	if content, ok := message.Content.(string); ok {
		trimmed := strings.TrimSpace(content)
		if trimmed == "" {
			message.Content = nil
		} else {
			message.Content = trimmed
		}
	}
	return message
}

func chatCompletionMessageEmpty(message chatCompletionsMessage) bool {
	if len(message.ToolCalls) > 0 {
		return false
	}
	if strings.TrimSpace(message.ToolCallID) != "" {
		return false
	}
	if text, ok := message.Content.(string); ok {
		return strings.TrimSpace(text) == ""
	}
	return message.Content == nil
}

func mergeChatCompletionTextPair(
	previous chatCompletionsMessage,
	current chatCompletionsMessage,
) (chatCompletionsMessage, bool) {
	role := normalizeChatCompletionRole(previous.Role)
	if role != normalizeChatCompletionRole(current.Role) {
		return chatCompletionsMessage{}, false
	}
	if role != "user" && role != "assistant" {
		return chatCompletionsMessage{}, false
	}
	if len(previous.ToolCalls) > 0 || len(current.ToolCalls) > 0 {
		return chatCompletionsMessage{}, false
	}
	if strings.TrimSpace(previous.ToolCallID) != "" || strings.TrimSpace(current.ToolCallID) != "" {
		return chatCompletionsMessage{}, false
	}
	previousText, previousOK := previous.Content.(string)
	currentText, currentOK := current.Content.(string)
	if !previousOK || !currentOK {
		return chatCompletionsMessage{}, false
	}
	if strings.TrimSpace(previousText) == "" || strings.TrimSpace(currentText) == "" {
		return chatCompletionsMessage{}, false
	}
	previous.Content = strings.TrimSpace(previousText) + "\n\n" + strings.TrimSpace(currentText)
	return previous, true
}

func (r chatCompletionsResponse) text() string {
	if len(r.Choices) == 0 {
		return ""
	}
	return extractOpenAIContent(r.Choices[0].Message.Content)
}

func (r chatCompletionsResponse) toolCalls() ([]model.ToolCall, error) {
	if len(r.Choices) == 0 {
		return nil, nil
	}
	rawCalls := repairChatCompletionToolCalls(r.Choices[0].Message.ToolCalls)
	calls := make([]model.ToolCall, 0, len(rawCalls))
	for _, rawCall := range rawCalls {
		name := strings.TrimSpace(rawCall.Function.Name)
		if name == "" {
			continue
		}
		args, err := parseToolArguments(rawCall.Function.Arguments)
		if err != nil {
			return nil, fmt.Errorf("parse tool arguments for %s: %w", name, err)
		}
		calls = append(calls, model.ToolCall{
			ID:    rawCall.ID,
			Name:  name,
			Input: args,
		})
	}
	return calls, nil
}

func (r chatCompletionsResponse) replayMessage() (chatCompletionsMessage, bool) {
	if len(r.Choices) == 0 {
		return chatCompletionsMessage{}, false
	}
	message := r.Choices[0].Message
	replayToolCalls := repairChatCompletionReplayToolCalls(message.ToolCalls)
	text := strings.TrimSpace(extractOpenAIContent(message.Content))
	if len(replayToolCalls) == 0 && text == "" {
		return chatCompletionsMessage{}, false
	}
	var content any = nil
	if text != "" {
		content = text
	}
	return chatCompletionsMessage{
		Role:      "assistant",
		Content:   content,
		ToolCalls: replayToolCalls,
	}, true
}

func repairChatCompletionReplayToolCalls(rawCalls []chatCompletionsToolCall) []chatCompletionsToolCall {
	repairedCalls := repairChatCompletionToolCalls(rawCalls)
	out := make([]chatCompletionsToolCall, 0, len(repairedCalls))
	for _, rawCall := range repairedCalls {
		name := strings.TrimSpace(rawCall.Function.Name)
		if name == "" {
			continue
		}
		out = append(out, chatCompletionsToolCall{
			ID:   rawCall.ID,
			Type: "function",
			Function: chatCompletionsToolCallFunction{
				Name:      name,
				Arguments: rawCall.Function.Arguments,
			},
		})
	}
	return out
}

func executeChatCompletionToolCalls(
	ctx context.Context,
	calls []model.ToolCall,
	executor model.ToolExecutor,
) ([]chatCompletionsMessage, error) {
	results := make([]chatCompletionsMessage, 0, len(calls))
	for _, call := range calls {
		result, err := executor(ctx, call)
		if err != nil {
			return nil, err
		}
		results = append(results, chatCompletionsMessage{
			Role:       "tool",
			Content:    result.Content,
			ToolCallID: authoritativeToolCallID(call, result),
		})
	}
	return results, nil
}
