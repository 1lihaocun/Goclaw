package feishu

func MarkdownCardContentJSON(text string) string {
	return string(mustJSON(map[string]any{
		"schema": "2.0",
		"config": map[string]any{
			"wide_screen_mode": true,
		},
		"body": map[string]any{
			"elements": []map[string]any{
				{
					"tag":     "markdown",
					"content": text,
				},
			},
		},
	}))
}

func StreamingMarkdownCardJSON(text string) string {
	return string(mustJSON(map[string]any{
		"schema": "2.0",
		"config": map[string]any{
			"streaming_mode": true,
			"summary": map[string]any{
				"content": "[Generating...]",
			},
			"streaming_config": map[string]any{
				"print_frequency_ms": map[string]any{
					"default": 50,
				},
				"print_step": map[string]any{
					"default": 1,
				},
			},
		},
		"body": map[string]any{
			"elements": []map[string]any{
				{
					"tag":        "markdown",
					"content":    text,
					"element_id": "content",
				},
			},
		},
	}))
}

func CardReferenceContentJSON(cardID string) string {
	return string(mustJSON(map[string]any{
		"type": "card",
		"data": map[string]any{
			"card_id": cardID,
		},
	}))
}
