package noop

import (
	"context"

	"goclaw/internal/memory"
)

type Provider struct{}

func New() *Provider {
	return &Provider{}
}

func (p *Provider) Name() string {
	return "noop"
}

func (p *Provider) Search(_ context.Context, _ memory.Scope, _ memory.SearchQuery) ([]memory.SearchResult, error) {
	return nil, nil
}

func (p *Provider) Write(_ context.Context, _ memory.Scope, _ []memory.WriteRecord) error {
	return nil
}

func (p *Provider) Health(_ context.Context) memory.HealthStatus {
	return memory.HealthStatus{
		Available: true,
		Detail:    "memory disabled",
	}
}
