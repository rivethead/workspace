// Package engine moves workflow tickets through their workflows. It is
// deterministic: the same event log and the same board give the same action,
// and no language model decides anything.
//
// The rules it keeps:
//   - A ticket's event log, in its metadata, is the only workflow state.
//   - Only the engine starts an agent run, by assigning the role's agent.
//     Outside an agent state, the ticket is unassigned.
//   - A status change never starts a run, and the engine posts a comment on
//     an assigned ticket only when it means to wake the agent.
//   - Each pass advances a ticket by at most one transition.
package engine

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/example-org/workspace/orchestrator/internal/tracker"
	"github.com/example-org/workspace/orchestrator/internal/workflow"
)

// Metadata keys on a ticket.
const (
	// KeyWorkflow names the workflow a ticket runs. A person sets it to
	// start the workflow.
	KeyWorkflow = "workflow"
	// KeyLog holds the event log, as JSON.
	KeyLog = "statechart"
)

// StatusWorking is the board status of a ticket that an agent works on.
const StatusWorking = "in_progress"

// maxLog keeps the event log well under the tracker's metadata size limit.
// Only the last entry decides what happens next; older ones are history.
const maxLog = 50

// Entry is one transition in a ticket's event log.
type Entry struct {
	Event string `json:"event"`
	To    string `json:"to"`
	// Seen is the number of comments on the ticket when the state was
	// entered. Only later comments can hold the agent's result.
	Seen int `json:"seen"`
}

// Engine runs the workflows of one workspace.
type Engine struct {
	Board     tracker.Tracker
	Workflows map[string]*workflow.Definition
	// Log receives one line per action and per contained error.
	Log func(format string, args ...any)
}

// Pass processes every ticket that runs a workflow, one at a time. An error
// on one ticket is logged and skips that ticket for this pass; it never stops
// the others.
func (e *Engine) Pass() error {
	agents, err := e.Board.Agents()
	if err != nil {
		return fmt.Errorf("listing agents: %w", err)
	}
	tickets, err := e.Board.Tickets()
	if err != nil {
		return fmt.Errorf("listing tickets: %w", err)
	}
	for _, t := range tickets {
		name, _ := t.Metadata[KeyWorkflow].(string)
		if name == "" {
			continue
		}
		if err := e.Step(t, agents); err != nil {
			e.logf("%s: %v", t.Identifier, err)
		}
	}
	return nil
}

// Step advances one ticket by at most one transition.
func (e *Engine) Step(t tracker.Ticket, agents map[string]string) error {
	name, _ := t.Metadata[KeyWorkflow].(string)
	def, ok := e.Workflows[name]
	if !ok {
		return fmt.Errorf("unknown workflow %q", name)
	}
	log, err := ReadLog(t)
	if err != nil {
		return err
	}
	if len(log) == 0 {
		return e.enter(t, def, agents, log, def.Initial, "start")
	}
	cur := log[len(log)-1]
	st, ok := def.States[cur.To]
	if !ok {
		return fmt.Errorf("the ticket is in state %q, which workflow %s no longer has", cur.To, def.Workflow)
	}
	switch st.Kind {
	case workflow.KindAgent:
		return e.stepAgent(t, def, agents, log, cur, st)
	case workflow.KindHuman:
		return e.stepHuman(t, def, agents, log, st)
	}
	return nil // a final state: nothing more happens
}

func (e *Engine) stepAgent(t tracker.Ticket, def *workflow.Definition, agents map[string]string, log []Entry, cur Entry, st workflow.State) error {
	agentID, ok := agents[st.Role]
	if !ok {
		return fmt.Errorf("no agent for role %s", st.Role)
	}
	comments, err := e.Board.Comments(t.ID)
	if err != nil {
		return err
	}
	if res, ok := lastResult(comments, cur.Seen, agentID); ok {
		event := res.Outcome
		if _, accepted := st.On[event]; !accepted {
			e.logf("%s: outcome %q is not one %s accepts; taking %s", t.Identifier, event, cur.To, workflow.EventFailed)
			event = workflow.EventFailed
		}
		return e.enter(t, def, agents, log, st.On[event], event)
	}
	// No result yet. Put back what a person or a crash changed: the agent
	// keeps the ticket, and the status stays the engine's.
	if t.AssigneeID != agentID {
		e.logf("%s: assigning %s again", t.Identifier, st.Role)
		if err := e.Board.Assign(t.ID, agentID); err != nil {
			return err
		}
	}
	if t.Status != StatusWorking {
		e.logf("%s: putting the status back to %s", t.Identifier, StatusWorking)
		return e.Board.SetStatus(t.ID, StatusWorking)
	}
	return nil
}

func (e *Engine) stepHuman(t tracker.Ticket, def *workflow.Definition, agents map[string]string, log []Entry, st workflow.State) error {
	if t.AssigneeID != "" {
		if err := e.Board.Unassign(t.ID); err != nil {
			return err
		}
	}
	if t.Status == workflow.StatusWaiting {
		return nil
	}
	if target, ok := st.On[t.Status]; ok {
		return e.enter(t, def, agents, log, target, t.Status)
	}
	e.logf("%s: refusing the move to %s", t.Identifier, t.Status)
	if err := e.Board.SetStatus(t.ID, workflow.StatusWaiting); err != nil {
		return err
	}
	return e.Board.AddComment(t.ID, fmt.Sprintf("Moving this ticket to %s does nothing here, so I put it back. Accepted moves: %s.", t.Status, strings.Join(st.Events(), ", ")))
}

// enter moves the ticket into a state and runs the state's entry action. The
// log is saved before the agent is assigned, so a pass that stops halfway
// resumes in the right state, and the next pass assigns the agent.
func (e *Engine) enter(t tracker.Ticket, def *workflow.Definition, agents map[string]string, log []Entry, name, event string) error {
	st := def.States[name]
	e.logf("%s: %s -> %s", t.Identifier, event, name)

	var agentID string
	if st.Kind == workflow.KindAgent {
		var ok bool
		if agentID, ok = agents[st.Role]; !ok {
			return fmt.Errorf("no agent for role %s", st.Role)
		}
	}
	// Unassign first, so the comment below wakes nobody.
	if t.AssigneeID != "" {
		if err := e.Board.Unassign(t.ID); err != nil {
			return err
		}
	}

	var status, comment string
	switch st.Kind {
	case workflow.KindAgent:
		status, comment = StatusWorking, task(t, st)
	case workflow.KindHuman:
		status, comment = workflow.StatusWaiting, waiting(st)
	case workflow.KindFinal:
		status, comment = st.FinalStatus(), "This workflow is finished."
	}
	if err := e.Board.AddComment(t.ID, comment); err != nil {
		return err
	}
	if err := e.Board.SetStatus(t.ID, status); err != nil {
		return err
	}
	comments, err := e.Board.Comments(t.ID)
	if err != nil {
		return err
	}
	log = append(log, Entry{Event: event, To: name, Seen: len(comments)})
	if err := e.saveLog(t, log); err != nil {
		return err
	}
	if st.Kind == workflow.KindAgent {
		return e.Board.Assign(t.ID, agentID)
	}
	return nil
}

func task(t tracker.Ticket, st workflow.State) string {
	body := strings.ReplaceAll(strings.TrimSpace(st.Task), "{ticket}", t.Identifier)
	return fmt.Sprintf("%s\n\nWhen you finish, end your last comment with a fenced `%s` block that holds JSON, for example `{\"outcome\": \"done\"}`. Outcomes this step accepts: %s.",
		body, ResultFence, strings.Join(st.Events(), ", "))
}

func waiting(st workflow.State) string {
	text := strings.TrimSpace(st.Description)
	if text == "" {
		text = "This ticket waits for a person."
	}
	return fmt.Sprintf("%s\n\nMove the ticket to one of: %s.", text, strings.Join(st.Events(), ", "))
}

// lastResult finds the agent's latest result after the first `seen`
// comments. Only the assigned agent's own comments count, so the task, which
// shows the result format, never counts as a result.
func lastResult(comments []tracker.Comment, seen int, agentID string) (Result, bool) {
	for i := len(comments) - 1; i >= seen && i >= 0; i-- {
		c := comments[i]
		if c.AuthorType != "agent" || c.AuthorID != agentID {
			continue
		}
		if r, ok := ParseResult(c.Content); ok {
			return r, true
		}
	}
	return Result{}, false
}

// ReadLog reads a ticket's event log. The tracker can return the metadata
// value as a JSON string or as decoded JSON; both are read.
func ReadLog(t tracker.Ticket) ([]Entry, error) {
	raw, ok := t.Metadata[KeyLog]
	if !ok || raw == nil {
		return nil, nil
	}
	var data []byte
	if s, isString := raw.(string); isString {
		data = []byte(s)
	} else {
		var err error
		if data, err = json.Marshal(raw); err != nil {
			return nil, err
		}
	}
	var log []Entry
	if err := json.Unmarshal(data, &log); err != nil {
		return nil, fmt.Errorf("reading the event log: %w", err)
	}
	return log, nil
}

func (e *Engine) saveLog(t tracker.Ticket, log []Entry) error {
	if len(log) > maxLog {
		log = log[len(log)-maxLog:]
	}
	data, err := json.Marshal(log)
	if err != nil {
		return err
	}
	return e.Board.SetMetadata(t.ID, KeyLog, string(data))
}

func (e *Engine) logf(format string, args ...any) {
	if e.Log != nil {
		e.Log(format, args...)
	}
}
