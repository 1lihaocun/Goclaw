package model

import "context"

type API string

const (
	APIOpenAICompletions    API = "openai-completions"
	APIOpenAIResponses      API = "openai-responses"
	APIOpenAICodexResponses API = "openai-codex-responses"
	APIAnthropicMessages    API = "anthropic-messages"
	APIGoogleGenerative     API = "google-generative-ai"
	APIOllama               API = "ollama"
)

type Message struct {
	Role    string
	Content string
}

type Request struct {
	System   string
	Messages []Message
}

type Provider interface {
	Name() string
	Generate(context.Context, Request) (string, error)
}
