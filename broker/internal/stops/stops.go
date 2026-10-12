// Package stops is the stop switch (ARCHITECTURE.md D35): a sponsor or an admin
// stops an agent, and the broker refuses it on its next request. It lives outside
// git, so stopping an agent needs no merge.
//
// Every stop and every start is also kept as an event that is never changed, so
// who stopped an agent and who started it again stays on record
// (docs/threat-model.md, TM-R1).
package stops

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Stop records who stopped an agent, when and why.
type Stop struct {
	Agent  string
	By     string
	Reason string
	At     time.Time
}

// Actions in the history.
const (
	ActionStop  = "stop"
	ActionStart = "start"
)

// Event is one stop or start of an agent.
type Event struct {
	Agent  string
	Action string
	By     string
	Reason string // empty for a start
	At     time.Time
}

// ErrNoName refuses a stop or a start that does not say who did it.
var ErrNoName = errors.New("say who stops or starts the agent")

type Store interface {
	// Stopped returns the agent's stop, if it is stopped.
	Stopped(ctx context.Context, agent string) (Stop, bool, error)
	// Set stops the agent (or updates its stop) and records the event.
	Set(ctx context.Context, s Stop) error
	// Clear starts the agent again, if it is stopped, and records who did it.
	Clear(ctx context.Context, agent, by string) error
	// History returns the agent's stops and starts, oldest first.
	History(ctx context.Context, agent string) ([]Event, error)
}

// Memory is a Store for tests.
type Memory struct {
	mu     sync.Mutex
	stops  map[string]Stop
	events []Event
}

func NewMemory() *Memory { return &Memory{stops: map[string]Stop{}} }

func (m *Memory) Stopped(_ context.Context, agent string) (Stop, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.stops[agent]
	return s, ok, nil
}

func (m *Memory) Set(_ context.Context, s Stop) error {
	if s.By == "" {
		return ErrNoName
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s.At = time.Now()
	m.stops[s.Agent] = s
	m.events = append(m.events, Event{Agent: s.Agent, Action: ActionStop, By: s.By, Reason: s.Reason, At: s.At})
	return nil
}

func (m *Memory) Clear(_ context.Context, agent, by string) error {
	if by == "" {
		return ErrNoName
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.stops[agent]; !ok {
		return nil
	}
	delete(m.stops, agent)
	m.events = append(m.events, Event{Agent: agent, Action: ActionStart, By: by, At: time.Now()})
	return nil
}

func (m *Memory) History(_ context.Context, agent string) ([]Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Event
	for _, e := range m.events {
		if e.Agent == agent {
			out = append(out, e)
		}
	}
	return out, nil
}
