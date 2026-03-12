package openaicompat

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

type responsesReasoningItem struct {
	Type             string `json:"type"`
	Content          string `json:"content,omitempty"`
	EncryptedContent string `json:"encrypted_content,omitempty"`
	Summary          string `json:"summary,omitempty"`
}

func repairResponsesReplayItems(output []responsesOutputItem) []any {
	items := make([]any, 0, len(output))
	seenReplayableReasoning := false
	functionCallIndex := 0

	for _, item := range output {
		switch item.Type {
		case "message":
			text := strings.TrimSpace(item.text())
			if text == "" {
				continue
			}
			items = append(items, responsesMessageItem{
				Type:    "message",
				Role:    "assistant",
				Content: text,
			})
		case "reasoning":
			replayItem, ok := replayableResponsesReasoningItem(item)
			if !ok {
				continue
			}
			seenReplayableReasoning = true
			items = append(items, replayItem)
		case "function_call":
			if strings.TrimSpace(item.Name) == "" {
				continue
			}
			callID := repairResponsesCallID(item, functionCallIndex)
			functionID := repairResponsesFunctionID(item, seenReplayableReasoning)
			items = append(items, responsesFunctionCallItem{
				Type:      "function_call",
				ID:        functionID,
				CallID:    callID,
				Name:      strings.TrimSpace(item.Name),
				Arguments: normalizeToolArgumentString(item.Arguments),
			})
			functionCallIndex++
		}
	}

	return items
}

func replayableResponsesReasoningItem(item responsesOutputItem) (responsesReasoningItem, bool) {
	replayItem := responsesReasoningItem{
		Type:             "reasoning",
		Content:          item.reasoningContent(),
		EncryptedContent: strings.TrimSpace(item.EncryptedContent),
		Summary:          strings.TrimSpace(item.Summary),
	}
	if replayItem.Content == "" && replayItem.EncryptedContent == "" && replayItem.Summary == "" {
		return responsesReasoningItem{}, false
	}
	return replayItem, true
}

func repairResponsesCallID(item responsesOutputItem, functionCallIndex int) string {
	if callID := firstNonEmpty(item.CallID, item.ID); callID != "" {
		return callID
	}
	return fmt.Sprintf("call_auto_%d", functionCallIndex+1)
}

func repairResponsesFunctionID(item responsesOutputItem, seenReplayableReasoning bool) string {
	functionID := strings.TrimSpace(item.ID)
	if functionID == "" {
		return ""
	}
	if seenReplayableReasoning || !strings.HasPrefix(functionID, "fc_") {
		return functionID
	}
	return ""
}

const strictToolCallIDMaxLen = 40

func repairChatCompletionToolCalls(rawCalls []chatCompletionsToolCall) []chatCompletionsToolCall {
	repaired := make([]chatCompletionsToolCall, 0, len(rawCalls))
	usedIDs := make(map[string]struct{}, len(rawCalls))
	for index, rawCall := range rawCalls {
		repaired = append(repaired, chatCompletionsToolCall{
			ID:   repairChatCompletionToolCallID(rawCall.ID, index, usedIDs),
			Type: "function",
			Function: chatCompletionsToolCallFunction{
				Name:      strings.TrimSpace(rawCall.Function.Name),
				Arguments: normalizeToolArgumentString(rawCall.Function.Arguments),
			},
		})
	}
	return repaired
}

func repairChatCompletionToolCallID(rawID string, index int, usedIDs map[string]struct{}) string {
	base := sanitizeStrictToolCallID(firstNonEmpty(rawID, fmt.Sprintf("call_auto_%d", index+1)))
	if base == "" {
		base = sanitizeStrictToolCallID(fmt.Sprintf("call_auto_%d", index+1))
	}
	if base == "" {
		base = "callauto"
	}
	base = truncateToolCallID(base)
	if _, exists := usedIDs[base]; !exists {
		usedIDs[base] = struct{}{}
		return base
	}

	hash := shortToolCallIDHash(firstNonEmpty(rawID, strconv.Itoa(index)))
	maxBaseLen := strictToolCallIDMaxLen - len(hash)
	if maxBaseLen < 1 {
		maxBaseLen = 1
	}
	candidate := truncateToolCallID(base)
	if len(candidate) > maxBaseLen {
		candidate = candidate[:maxBaseLen]
	}
	candidate += hash
	if _, exists := usedIDs[candidate]; !exists {
		usedIDs[candidate] = struct{}{}
		return candidate
	}

	for suffixIndex := 2; suffixIndex < 1000; suffixIndex++ {
		suffix := "x" + strconv.Itoa(suffixIndex)
		next := candidate
		if len(next) > strictToolCallIDMaxLen-len(suffix) {
			next = next[:strictToolCallIDMaxLen-len(suffix)]
		}
		next += suffix
		if _, exists := usedIDs[next]; !exists {
			usedIDs[next] = struct{}{}
			return next
		}
	}

	fallback := truncateToolCallID(candidate + "overflow")
	usedIDs[fallback] = struct{}{}
	return fallback
}

func sanitizeStrictToolCallID(value string) string {
	var builder strings.Builder
	builder.Grow(len(value))
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			builder.WriteRune(r)
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

func truncateToolCallID(value string) string {
	if len(value) <= strictToolCallIDMaxLen {
		return value
	}
	return value[:strictToolCallIDMaxLen]
}

func shortToolCallIDHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:8]
}
