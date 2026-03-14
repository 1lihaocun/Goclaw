package model

import "context"

type StreamEventType string

const (
	StreamEventAssistantDelta StreamEventType = "assistant_delta"
)

type StreamEvent struct {
	Type StreamEventType
	Text string
}

type StreamHandler func(StreamEvent) error

type StreamProvider interface {
	Stream(context.Context, Request, StreamHandler) (string, error)
}

type StreamSupportReporter interface {
	SupportsStreaming() bool
}

func SupportsStreaming(provider Provider) bool {
	_, ok := StreamProviderFor(provider)
	return ok
}

func StreamProviderFor(provider Provider) (StreamProvider, bool) {
	streamer, ok := provider.(StreamProvider)
	if !ok {
		return nil, false
	}
	reporter, ok := provider.(StreamSupportReporter)
	if !ok {
		return streamer, true
	}
	if !reporter.SupportsStreaming() {
		return nil, false
	}
	return streamer, true
}
