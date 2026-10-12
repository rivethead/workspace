package workflow_test

import (
	"strings"
	"testing"

	"github.com/example-org/workspace/orchestrator/internal/workflow"
)

const valid = `
schema: workflow/v1
workflow: tiny
initial: work
states:
  work:
    kind: agent
    role: implementer
    task: Do {ticket}.
    on: { done: check, failed: check }
  check:
    kind: human
    on: { done: end, todo: work }
  end:
    kind: final
`

func TestTheExampleWorkflowsAreValid(t *testing.T) {
	defs, err := workflow.LoadDir("../../examples/workflows")
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) == 0 {
		t.Fatal("no example workflows loaded")
	}
}

func TestAValidWorkflowParses(t *testing.T) {
	d, err := workflow.Parse([]byte(valid))
	if err != nil {
		t.Fatal(err)
	}
	if d.States["end"].FinalStatus() != "done" {
		t.Errorf("a final state without a status should end in done, got %q", d.States["end"].FinalStatus())
	}
}

func TestInvalidWorkflowsAreRefused(t *testing.T) {
	cases := map[string]struct{ from, to, want string }{
		"unknown key":             {"kind: final", "kind: final\n    colour: red", "colour"},
		"wrong schema":            {"workflow/v1", "workflow/v2", "schema"},
		"missing target":          {"done: end, todo: work", "done: nowhere, todo: work", "nowhere"},
		"agent without failed":    {"{ done: check, failed: check }", "{ done: check }", `must handle "failed"`},
		"human with a non-move":   {"{ done: end, todo: work }", "{ approve: end, todo: work }", "not a board move"},
		"unknown kind":            {"kind: human", "kind: robot", "kind must be"},
		"unreachable state":       {"{ done: end, todo: work }", "{ todo: work }", "cannot be reached"},
		"agent without a role":    {"role: implementer", "role: \"\"", "needs a role"},
		"initial does not exist":  {"initial: work", "initial: start", "initial state"},
		"final with a bad status": {"kind: final", "kind: final\n    status: blocked", "done or cancelled"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			src := strings.Replace(valid, c.from, c.to, 1)
			_, err := workflow.Parse([]byte(src))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want an error with %q, got %v", c.want, err)
			}
		})
	}
}
