package runtime

import (
	"context"
	"sync"

	channelcore "goclaw/internal/channels"
	"goclaw/internal/domain"
)

type inboundMessageIngestor interface {
	IngestInboundMessage(ctx context.Context, message channelcore.InboundMessage) error
}

type channelEventObserver interface {
	ObserveChannelEvent(ctx context.Context, event channelcore.ChannelEvent) error
}

type serializedRoomExecutor struct {
	mu   sync.Mutex
	keys map[string]chan struct{}
}

func newSerializedRoomExecutor() *serializedRoomExecutor {
	return &serializedRoomExecutor{
		keys: make(map[string]chan struct{}),
	}
}

func (e *serializedRoomExecutor) Execute(ctx context.Context, key string, fn func() error) error {
	if key == "" {
		return fn()
	}

	wait := e.acquire(key)
	if wait != nil {
		select {
		case <-wait:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	done := make(chan struct{})
	e.set(key, done)
	err := fn()
	e.finish(key, done)
	return err
}

func (e *serializedRoomExecutor) acquire(key string) chan struct{} {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.keys[key]
}

func (e *serializedRoomExecutor) set(key string, done chan struct{}) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.keys[key] = done
}

func (e *serializedRoomExecutor) finish(key string, done chan struct{}) {
	close(done)
	e.mu.Lock()
	defer e.mu.Unlock()
	if current, ok := e.keys[key]; ok && current == done {
		delete(e.keys, key)
	}
}

type serializedInboundIngestor struct {
	executor *serializedRoomExecutor
	next     inboundMessageIngestor
}

func newSerializedInboundIngestor(
	executor *serializedRoomExecutor,
	next inboundMessageIngestor,
) *serializedInboundIngestor {
	if executor == nil {
		executor = newSerializedRoomExecutor()
	}
	return &serializedInboundIngestor{
		executor: executor,
		next:     next,
	}
}

func (s *serializedInboundIngestor) IngestInboundMessage(
	ctx context.Context,
	message channelcore.InboundMessage,
) error {
	key := serializeKeyForRoom(message.Provider, message.ProfileID, message.RoomID)
	return s.executor.Execute(ctx, key, func() error {
		return s.next.IngestInboundMessage(ctx, message)
	})
}

type serializedChannelEventObserver struct {
	executor *serializedRoomExecutor
	next     channelEventObserver
}

func newSerializedChannelEventObserver(
	executor *serializedRoomExecutor,
	next channelEventObserver,
) *serializedChannelEventObserver {
	if executor == nil {
		executor = newSerializedRoomExecutor()
	}
	return &serializedChannelEventObserver{
		executor: executor,
		next:     next,
	}
}

func (s *serializedChannelEventObserver) ObserveChannelEvent(
	ctx context.Context,
	event channelcore.ChannelEvent,
) error {
	key := serializeKeyForEvent(event)
	return s.executor.Execute(ctx, key, func() error {
		return s.next.ObserveChannelEvent(ctx, event)
	})
}

func serializeKeyForEvent(event channelcore.ChannelEvent) string {
	if key := serializeKeyForRoom(event.Provider, event.ProfileID, event.RoomID); key != "" {
		return key
	}
	if event.Provider != "" && event.ProfileID != "" && event.ProviderMessageID != "" {
		return string(event.Provider) + ":" + string(event.ProfileID) + ":message:" + event.ProviderMessageID
	}
	if event.Provider != "" && event.ProfileID != "" && event.EventID != "" {
		return string(event.Provider) + ":" + string(event.ProfileID) + ":event:" + event.EventID
	}
	return ""
}

func serializeKeyForRoom(
	provider domain.Provider,
	profileID domain.ProfileID,
	roomID domain.RoomID,
) string {
	if provider == "" || profileID == "" || roomID == "" {
		return ""
	}
	return string(provider) + ":" + string(profileID) + ":" + string(roomID)
}
