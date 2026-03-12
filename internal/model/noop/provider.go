package noop

import (
	"context"

	"goclaw/internal/model"
)

type Provider struct{}

func New() *Provider {
	return &Provider{}
}

func (p *Provider) Name() string {
	return "noop"
}

func (p *Provider) Generate(_ context.Context, _ model.Request) (string, error) {
	return "", nil
}
