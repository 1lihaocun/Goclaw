package longpoll

import (
	"sync"
	"time"
)

type State struct {
	Running     bool
	LastEventAt time.Time
	LastError   string
}

type stateStore struct {
	mu    sync.RWMutex
	state State
}

func (s *stateStore) snapshot() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

func (s *stateStore) setRunning(running bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Running = running
}

func (s *stateStore) setLastEventAt(at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.LastEventAt = at.UTC()
}

func (s *stateStore) setError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil {
		s.state.LastError = ""
		return
	}
	s.state.LastError = err.Error()
}
