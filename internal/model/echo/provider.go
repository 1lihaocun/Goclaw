package echo

import (
	"context"
	"strings"

	"goclaw/internal/model"
)

type Provider struct{}

func New() *Provider {
	return &Provider{}
}

func (p *Provider) Name() string {
	return "echo"
}

func (p *Provider) Generate(_ context.Context, request model.Request) (string, error) {
	for i := len(request.Messages) - 1; i >= 0; i-- {
		content := strings.TrimSpace(request.Messages[i].Content)
		if request.Messages[i].Role == "user" && content != "" {
			return "Echo: " + content, nil
		}
	}
	return "", nil
}
