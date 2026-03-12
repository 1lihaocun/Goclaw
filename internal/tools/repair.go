package tools

import (
	"encoding/json"
	"fmt"
	"strings"
)

func ParseLegacyOutput(output string) AssistantTurn {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return AssistantTurn{
			StopReason: StopReasonFinal,
			Blocks: []AssistantBlock{
				TextBlock{Text: ""},
			},
		}
	}

	envelope, ok := repairLegacyEnvelope(trimmed)
	if !ok || envelope.Type != "tool_call" {
		content := trimmed
		if ok && strings.TrimSpace(envelope.Content) != "" {
			content = strings.TrimSpace(envelope.Content)
		}
		return AssistantTurn{
			StopReason: StopReasonFinal,
			Blocks: []AssistantBlock{
				TextBlock{Text: content},
			},
		}
	}

	return RepairAssistantTurn(AssistantTurn{
		StopReason: StopReasonToolUse,
		Blocks: []AssistantBlock{
			ToolCallBlock{
				Name: strings.TrimSpace(envelope.Tool),
				Args: append(json.RawMessage(nil), envelope.Args...),
			},
		},
	})
}

func RepairAssistantTurn(turn AssistantTurn) AssistantTurn {
	if len(turn.Blocks) == 0 {
		return turn
	}

	normalized := AssistantTurn{
		StopReason: turn.StopReason,
		Blocks:     make([]AssistantBlock, 0, len(turn.Blocks)),
	}
	autoID := 1
	for _, block := range turn.Blocks {
		switch typed := block.(type) {
		case TextBlock:
			normalized.Blocks = append(normalized.Blocks, TextBlock{
				Text: strings.TrimSpace(typed.Text),
			})
		case ToolCallBlock:
			name := strings.TrimSpace(typed.Name)
			if name == "" {
				continue
			}
			id := strings.TrimSpace(typed.ID)
			if id == "" {
				id = fmt.Sprintf("call_auto_%d", autoID)
				autoID++
			}
			normalized.Blocks = append(normalized.Blocks, ToolCallBlock{
				ID:   id,
				Name: name,
				Args: normalizeArgsJSON(typed.Args),
			})
		default:
			normalized.Blocks = append(normalized.Blocks, block)
		}
	}
	return normalized
}

func NormalizeAssistantTurn(turn AssistantTurn) AssistantTurn {
	return RepairAssistantTurn(turn)
}

func repairLegacyEnvelope(output string) (legacyEnvelope, bool) {
	for _, candidate := range legacyEnvelopeCandidates(output) {
		envelope, ok := decodeLegacyEnvelope(candidate)
		if !ok {
			continue
		}
		return envelope, true
	}
	return legacyEnvelope{}, false
}

func legacyEnvelopeCandidates(output string) []string {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return nil
	}
	candidates := []string{trimmed}
	if extracted, ok := extractJSONObject(trimmed); ok && extracted != trimmed {
		candidates = append(candidates, extracted)
	}
	return candidates
}

func decodeLegacyEnvelope(candidate string) (legacyEnvelope, bool) {
	var envelope legacyEnvelope
	if err := json.Unmarshal([]byte(candidate), &envelope); err != nil {
		return legacyEnvelope{}, false
	}

	switch strings.TrimSpace(envelope.Type) {
	case "tool_call":
		if strings.TrimSpace(envelope.Tool) == "" {
			return legacyEnvelope{}, false
		}
		return envelope, true
	case "final":
		content := strings.TrimSpace(envelope.Content)
		if content == "" {
			content = candidate
		}
		envelope.Content = content
		return envelope, true
	default:
		return legacyEnvelope{}, false
	}
}

func extractJSONObject(value string) (string, bool) {
	start := -1
	depth := 0
	inString := false
	escaped := false

	for i, r := range value {
		switch {
		case escaped:
			escaped = false
		case r == '\\' && inString:
			escaped = true
		case r == '"':
			inString = !inString
		case inString:
			continue
		case r == '{':
			if depth == 0 {
				start = i
			}
			depth++
		case r == '}':
			if depth == 0 {
				continue
			}
			depth--
			if depth == 0 && start >= 0 {
				return value[start : i+1], true
			}
		}
	}
	return "", false
}

func normalizeArgsJSON(value json.RawMessage) json.RawMessage {
	trimmed := strings.TrimSpace(string(value))
	if trimmed == "" {
		return json.RawMessage(`{}`)
	}
	return append(json.RawMessage(nil), trimmed...)
}
