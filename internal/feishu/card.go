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
