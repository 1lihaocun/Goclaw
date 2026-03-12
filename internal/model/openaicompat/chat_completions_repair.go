package openaicompat

import "strings"

func repairChatCompletionMessages(messages []chatCompletionsMessage) []chatCompletionsMessage {
	messages = normalizeChatCompletionMessages(messages)
	messages = repairChatCompletionToolMessagePairing(messages)
	messages = mergeAdjacentChatCompletionTextMessages(messages)

	firstConversationIndex := -1
	for index, message := range messages {
		if normalizeChatCompletionRole(message.Role) == "system" {
			continue
		}
		firstConversationIndex = index
		break
	}
	if firstConversationIndex == -1 {
		return messages
	}
	first := messages[firstConversationIndex]
	if normalizeChatCompletionRole(first.Role) == "user" {
		if text, ok := first.Content.(string); ok && strings.TrimSpace(text) == chatCompletionsBootstrapText {
			return messages
		}
	}
	if normalizeChatCompletionRole(first.Role) != "assistant" {
		return messages
	}

	bootstrap := chatCompletionsMessage{
		Role:    "user",
		Content: chatCompletionsBootstrapText,
	}
	out := make([]chatCompletionsMessage, 0, len(messages)+1)
	out = append(out, messages[:firstConversationIndex]...)
	out = append(out, bootstrap)
	out = append(out, messages[firstConversationIndex:]...)
	return out
}

func normalizeChatCompletionMessages(messages []chatCompletionsMessage) []chatCompletionsMessage {
	out := make([]chatCompletionsMessage, 0, len(messages))
	for _, message := range messages {
		normalized := normalizeChatCompletionMessage(message)
		if len(normalized.ToolCalls) > 0 {
			normalized.ToolCalls = repairChatCompletionReplayToolCalls(normalized.ToolCalls)
		}
		normalized.ToolCallID = normalizeChatCompletionToolResultID(normalized.ToolCallID)
		out = append(out, normalized)
	}
	return out
}

func repairChatCompletionToolMessagePairing(messages []chatCompletionsMessage) []chatCompletionsMessage {
	out := make([]chatCompletionsMessage, 0, len(messages))
	seenToolResults := make(map[string]struct{})

	for i := 0; i < len(messages); i++ {
		message := messages[i]
		role := normalizeChatCompletionRole(message.Role)
		if role != "assistant" {
			if role != "tool" {
				out = append(out, message)
			}
			continue
		}
		if len(message.ToolCalls) == 0 {
			out = append(out, message)
			continue
		}

		toolCalls := repairChatCompletionReplayToolCalls(message.ToolCalls)
		message.ToolCalls = toolCalls
		out = append(out, message)

		toolCallIDs := make(map[string]struct{}, len(toolCalls))
		resultsByID := make(map[string]chatCompletionsMessage, len(toolCalls))
		remainder := make([]chatCompletionsMessage, 0)
		for _, call := range toolCalls {
			if strings.TrimSpace(call.ID) == "" {
				continue
			}
			toolCallIDs[call.ID] = struct{}{}
		}

		j := i + 1
		for ; j < len(messages); j++ {
			next := messages[j]
			nextRole := normalizeChatCompletionRole(next.Role)
			if nextRole == "assistant" {
				break
			}
			if nextRole == "tool" {
				toolCallID := strings.TrimSpace(next.ToolCallID)
				if _, ok := toolCallIDs[toolCallID]; ok {
					if _, duplicated := seenToolResults[toolCallID]; duplicated {
						continue
					}
					if _, exists := resultsByID[toolCallID]; !exists {
						resultsByID[toolCallID] = next
					}
					continue
				}
				continue
			}
			remainder = append(remainder, next)
		}

		for _, call := range toolCalls {
			if result, ok := resultsByID[call.ID]; ok {
				seenToolResults[call.ID] = struct{}{}
				out = append(out, result)
				continue
			}
			if strings.TrimSpace(call.ID) == "" {
				continue
			}
			seenToolResults[call.ID] = struct{}{}
			out = append(out, syntheticChatCompletionToolResult(call.ID))
		}

		out = append(out, remainder...)
		i = j - 1
	}

	return out
}

func syntheticChatCompletionToolResult(toolCallID string) chatCompletionsMessage {
	return chatCompletionsMessage{
		Role:       "tool",
		ToolCallID: toolCallID,
		Content:    `{"ok":false,"error":"missing tool result inserted for transcript repair"}`,
	}
}

func normalizeChatCompletionToolResultID(rawID string) string {
	trimmed := strings.TrimSpace(rawID)
	if trimmed == "" {
		return ""
	}
	normalized := truncateToolCallID(sanitizeStrictToolCallID(trimmed))
	if normalized == "" {
		return trimmed
	}
	return normalized
}
