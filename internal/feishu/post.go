package feishu

import (
	"encoding/json"
	"strings"
)

const fallbackPostText = "[Rich text message]"

type postPayload struct {
	Title   string          `json:"title"`
	Content [][]postElement `json:"content"`
}

type postElement map[string]any

func MarkdownPostContentJSON(text string) string {
	return string(mustJSON(map[string]any{
		"zh_cn": map[string]any{
			"content": [][]map[string]string{
				{
					{
						"tag":  "md",
						"text": text,
					},
				},
			},
		},
	}))
}

func parsePostText(content string) string {
	payload, ok := decodePostPayload(content)
	if !ok {
		return fallbackPostText
	}

	lines := make([]string, 0, len(payload.Content)+1)
	title := strings.TrimSpace(payload.Title)
	if title != "" {
		lines = append(lines, title)
	}

	for _, row := range payload.Content {
		parts := make([]string, 0, len(row))
		for _, element := range row {
			rendered := renderPostElement(element)
			if rendered != "" {
				parts = append(parts, rendered)
			}
		}
		line := strings.TrimSpace(strings.Join(parts, ""))
		if line != "" {
			lines = append(lines, line)
		}
	}

	text := strings.TrimSpace(strings.Join(lines, "\n"))
	if text == "" {
		return fallbackPostText
	}
	return text
}

func decodePostPayload(content string) (postPayload, bool) {
	var direct postPayload
	if err := json.Unmarshal([]byte(content), &direct); err == nil && len(direct.Content) > 0 {
		return direct, true
	}

	var envelope map[string]postPayload
	if err := json.Unmarshal([]byte(content), &envelope); err != nil {
		return postPayload{}, false
	}
	for _, payload := range envelope {
		if len(payload.Content) > 0 {
			return payload, true
		}
	}
	return postPayload{}, false
}

func renderPostElement(element postElement) string {
	tag := strings.ToLower(stringValue(element["tag"]))
	switch tag {
	case "md":
		return stringValue(element["text"])
	case "text":
		return stringValue(element["text"])
	case "a":
		if text := stringValue(element["text"]); text != "" {
			return text
		}
		return stringValue(element["href"])
	case "at":
		name := firstNonEmpty(
			stringValue(element["user_name"]),
			stringValue(element["user_id"]),
			stringValue(element["open_id"]),
		)
		if name == "" {
			return ""
		}
		return "@" + name
	case "emotion":
		return firstNonEmpty(stringValue(element["emoji"]), stringValue(element["text"]))
	case "br":
		return "\n"
	case "code":
		if text := stringValue(element["text"]); text != "" {
			return wrapInlineCode(text)
		}
		return ""
	case "code_block", "pre":
		code := firstNonEmpty(stringValue(element["text"]), stringValue(element["content"]))
		if code == "" {
			return ""
		}
		language := sanitizeFenceLanguage(firstNonEmpty(
			stringValue(element["language"]),
			stringValue(element["lang"]),
		))
		if !strings.HasSuffix(code, "\n") {
			code += "\n"
		}
		return "```" + language + "\n" + code + "```"
	case "img":
		return "[image]"
	case "media":
		if fileName := stringValue(element["file_name"]); fileName != "" {
			return "[media: " + fileName + "]"
		}
		return "[media]"
	default:
		return firstNonEmpty(stringValue(element["text"]), stringValue(element["content"]))
	}
}

func wrapInlineCode(text string) string {
	maxRun := 0
	currentRun := 0
	for _, r := range text {
		if r == '`' {
			currentRun++
			if currentRun > maxRun {
				maxRun = currentRun
			}
			continue
		}
		currentRun = 0
	}
	fence := strings.Repeat("`", maxRun+1)
	if strings.HasPrefix(text, "`") || strings.HasSuffix(text, "`") {
		return fence + " " + text + " " + fence
	}
	return fence + text + fence
}

func sanitizeFenceLanguage(language string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(language) {
		if (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '_' || r == '+' || r == '#' || r == '.' || r == '-' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}
