// Package workflow loads and validates workflow files: a workflow is a set of
// named states, and each state says who acts in it and which events move the
// ticket on.
package workflow

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Schema is the only schema this engine reads.
const Schema = "workflow/v1"

// The kinds of state.
const (
	KindAgent = "agent" // one agent role works; it reports an outcome
	KindHuman = "human" // the ticket waits for a person to move it on the board
	KindFinal = "final" // the workflow is over
)

// EventFailed is the event an agent state takes when the agent fails, or
// reports an outcome the state does not accept. Every agent state handles it.
const EventFailed = "failed"

// StatusWaiting is the board status of a ticket that waits for a person.
const StatusWaiting = "in_review"

// BoardMoves are the statuses a person can move a waiting ticket to. In a
// human state, the status a person picks is the event.
var BoardMoves = map[string]bool{"todo": true, "in_progress": true, "done": true, "cancelled": true, "blocked": true}

// Definition is one workflow file.
type Definition struct {
	Schema      string           `yaml:"schema"`
	Workflow    string           `yaml:"workflow"`
	Description string           `yaml:"description"`
	Initial     string           `yaml:"initial"`
	States      map[string]State `yaml:"states"`
}

// State is one state of a workflow.
type State struct {
	Kind        string            `yaml:"kind"`
	Description string            `yaml:"description"`
	Role        string            `yaml:"role"`   // agent: the role whose agent is assigned
	Task        string            `yaml:"task"`   // agent: the task written on the ticket
	Status      string            `yaml:"status"` // final: the board status at the end (default done)
	On          map[string]string `yaml:"on"`     // event -> next state
}

// Events returns the events a state accepts, sorted, so that comments and
// tasks list them the same way every time.
func (s State) Events() []string {
	out := make([]string, 0, len(s.On))
	for e := range s.On {
		out = append(out, e)
	}
	sort.Strings(out)
	return out
}

// FinalStatus is the board status a final state leaves the ticket in.
func (s State) FinalStatus() string {
	if s.Status == "" {
		return "done"
	}
	return s.Status
}

// Parse reads and validates one workflow. Unknown keys are an error, so a typo
// in a workflow is found when it loads, not when a ticket reaches it.
func Parse(data []byte) (*Definition, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var d Definition
	if err := dec.Decode(&d); err != nil {
		return nil, fmt.Errorf("reading the workflow: %w", err)
	}
	if err := d.Validate(); err != nil {
		return nil, err
	}
	return &d, nil
}

// LoadDir reads every *.yaml file in dir. One invalid file fails the load, so
// the engine never runs half of a set of workflows.
func LoadDir(dir string) (map[string]*Definition, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, err
	}
	defs := map[string]*Definition{}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		d, err := Parse(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		if _, dup := defs[d.Workflow]; dup {
			return nil, fmt.Errorf("%s: a second workflow named %q", p, d.Workflow)
		}
		defs[d.Workflow] = d
	}
	return defs, nil
}

// Validate checks the rules that the engine relies on. It returns every
// problem at once, so one run fixes a workflow.
func (d *Definition) Validate() error {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	if d.Schema != Schema {
		add("schema must be %q, not %q", Schema, d.Schema)
	}
	if d.Workflow == "" {
		add("the workflow has no name")
	}
	if _, ok := d.States[d.Initial]; !ok {
		add("the initial state %q does not exist", d.Initial)
	}
	finals := 0
	for _, name := range sortedNames(d.States) {
		s := d.States[name]
		for event, target := range s.On {
			if _, ok := d.States[target]; !ok {
				add("state %s: event %s goes to %q, which does not exist", name, event, target)
			}
		}
		switch s.Kind {
		case KindAgent:
			if s.Role == "" || strings.TrimSpace(s.Task) == "" {
				add("state %s: an agent state needs a role and a task", name)
			}
			if _, ok := s.On[EventFailed]; !ok {
				add("state %s: an agent state must handle %q", name, EventFailed)
			}
		case KindHuman:
			if len(s.On) == 0 {
				add("state %s: a human state needs at least one board move", name)
			}
			for event := range s.On {
				if !BoardMoves[event] {
					add("state %s: %q is not a board move (use todo, in_progress, done, cancelled or blocked)", name, event)
				}
			}
		case KindFinal:
			finals++
			if len(s.On) > 0 {
				add("state %s: a final state has no events", name)
			}
			if s.Status != "" && s.Status != "done" && s.Status != "cancelled" {
				add("state %s: a final status is done or cancelled, not %q", name, s.Status)
			}
		default:
			add("state %s: kind must be agent, human or final, not %q", name, s.Kind)
		}
	}
	if finals == 0 {
		add("the workflow has no final state")
	}
	for _, name := range unreachable(d) {
		add("state %s cannot be reached from %s", name, d.Initial)
	}
	if len(problems) > 0 {
		return fmt.Errorf("workflow %q is not valid:\n- %s", d.Workflow, strings.Join(problems, "\n- "))
	}
	return nil
}

func unreachable(d *Definition) []string {
	seen := map[string]bool{}
	var walk func(string)
	walk = func(name string) {
		s, ok := d.States[name]
		if !ok || seen[name] {
			return
		}
		seen[name] = true
		for _, target := range s.On {
			walk(target)
		}
	}
	walk(d.Initial)
	var out []string
	for _, name := range sortedNames(d.States) {
		if !seen[name] {
			out = append(out, name)
		}
	}
	return out
}

func sortedNames(states map[string]State) []string {
	out := make([]string, 0, len(states))
	for name := range states {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
