package tracker

import (
	"fmt"
	"sort"
)

// Memory is an in-memory board for tests. It behaves like Multica where the
// engine depends on it: assigning a ticket counts as starting a run, and a
// comment on an assigned ticket counts as waking its agent.
type Memory struct {
	tickets  map[string]*Ticket
	comments map[string][]Comment
	agents   map[string]string
	// Runs counts the runs started on each ticket, by assignment or by a
	// comment while assigned.
	Runs map[string]int
}

// NewMemory returns an empty board with the given agents (name -> ID).
func NewMemory(agents map[string]string) *Memory {
	return &Memory{
		tickets:  map[string]*Ticket{},
		comments: map[string][]Comment{},
		agents:   agents,
		Runs:     map[string]int{},
	}
}

// Add puts a ticket on the board.
func (m *Memory) Add(t Ticket) {
	if t.Metadata == nil {
		t.Metadata = map[string]any{}
	}
	m.tickets[t.ID] = &t
}

// Get returns a copy of a ticket.
func (m *Memory) Get(id string) Ticket {
	return *m.tickets[id]
}

// Move changes a ticket's status the way a person does on the board.
func (m *Memory) Move(id, status string) {
	m.tickets[id].Status = status
}

// Reply posts a comment as an agent, the way an agent ends its run.
func (m *Memory) Reply(id, agentID, text string) {
	m.comments[id] = append(m.comments[id], Comment{AuthorType: "agent", AuthorID: agentID, Content: text})
}

func (m *Memory) Tickets() ([]Ticket, error) {
	ids := make([]string, 0, len(m.tickets))
	for id := range m.tickets {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]Ticket, 0, len(ids))
	for _, id := range ids {
		out = append(out, *m.tickets[id])
	}
	return out, nil
}

func (m *Memory) Agents() (map[string]string, error) { return m.agents, nil }

func (m *Memory) Comments(id string) ([]Comment, error) {
	return append([]Comment(nil), m.comments[id]...), nil
}

func (m *Memory) AddComment(id, text string) error {
	t, err := m.ticket(id)
	if err != nil {
		return err
	}
	m.comments[id] = append(m.comments[id], Comment{AuthorType: "member", AuthorID: "orchestrator", Content: text})
	if t.AssigneeID != "" {
		m.Runs[id]++
	}
	return nil
}

func (m *Memory) Assign(id, agentID string) error {
	t, err := m.ticket(id)
	if err != nil {
		return err
	}
	t.AssigneeID = agentID
	m.Runs[id]++
	return nil
}

func (m *Memory) Unassign(id string) error {
	t, err := m.ticket(id)
	if err != nil {
		return err
	}
	t.AssigneeID = ""
	return nil
}

func (m *Memory) SetStatus(id, status string) error {
	t, err := m.ticket(id)
	if err != nil {
		return err
	}
	t.Status = status
	return nil
}

func (m *Memory) SetMetadata(id, key, value string) error {
	t, err := m.ticket(id)
	if err != nil {
		return err
	}
	t.Metadata[key] = value
	return nil
}

func (m *Memory) ticket(id string) (*Ticket, error) {
	t, ok := m.tickets[id]
	if !ok {
		return nil, fmt.Errorf("no ticket %s", id)
	}
	return t, nil
}
