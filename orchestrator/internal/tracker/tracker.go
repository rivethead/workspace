// Package tracker is the engine's only way to the board: tickets, comments,
// assignments, statuses and metadata. Multica implements it with the
// `multica` CLI; Memory implements it in memory for tests.
package tracker

// Ticket is one ticket on the board.
type Ticket struct {
	ID         string
	Identifier string // the key people see, for example WS-12
	Title      string
	Status     string
	AssigneeID string // empty when the ticket is unassigned
	Metadata   map[string]any
}

// Comment is one comment on a ticket, oldest first in a list.
type Comment struct {
	AuthorType string // "member" or "agent"
	AuthorID   string
	Content    string
}

// Tracker is what the engine needs from the board.
type Tracker interface {
	// Tickets lists the tickets of the workspace.
	Tickets() ([]Ticket, error)
	// Agents maps each agent's name to its ID. An agent's name is its role.
	Agents() (map[string]string, error)
	// Comments lists a ticket's comments, oldest first.
	Comments(ticketID string) ([]Comment, error)
	// AddComment posts a comment. On an assigned ticket it wakes the agent.
	AddComment(ticketID, text string) error
	// Assign gives the ticket to an agent, which starts a run.
	Assign(ticketID, agentID string) error
	// Unassign takes the ticket away from its agent.
	Unassign(ticketID string) error
	// SetStatus moves the ticket on the board. It never starts a run.
	SetStatus(ticketID, status string) error
	// SetMetadata writes one metadata key of the ticket.
	SetMetadata(ticketID, key, value string) error
}
