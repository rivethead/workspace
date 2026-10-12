package engine_test

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/example-org/workspace/orchestrator/internal/engine"
	"github.com/example-org/workspace/orchestrator/internal/tracker"
	"github.com/example-org/workspace/orchestrator/internal/workflow"
)

// The flows run the example `change` workflow pass by pass against the
// in-memory board.

var agents = map[string]string{"implementer": "agent-impl", "reviewer": "agent-rev"}

type harness struct {
	t     *testing.T
	board *tracker.Memory
	eng   *engine.Engine
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	data, err := os.ReadFile("../../examples/workflows/change.yaml")
	if err != nil {
		t.Fatal(err)
	}
	def, err := workflow.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	board := tracker.NewMemory(agents)
	board.Add(tracker.Ticket{ID: "1", Identifier: "WS-1", Status: "todo", Metadata: map[string]any{engine.KeyWorkflow: "change"}})
	return &harness{t: t, board: board, eng: &engine.Engine{
		Board:     board,
		Workflows: map[string]*workflow.Definition{"change": def},
		Log:       t.Logf,
	}}
}

func (h *harness) pass() {
	h.t.Helper()
	if err := h.eng.Pass(); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) state() string {
	h.t.Helper()
	log, err := engine.ReadLog(h.board.Get("1"))
	if err != nil {
		h.t.Fatal(err)
	}
	if len(log) == 0 {
		return ""
	}
	return log[len(log)-1].To
}

func (h *harness) reply(agentID, outcome string) {
	h.board.Reply("1", agentID, fmt.Sprintf("Finished.\n\n```workflow-result\n{\"outcome\": %q}\n```", outcome))
}

func (h *harness) expect(state, status, assignee string) {
	h.t.Helper()
	tk := h.board.Get("1")
	if got := h.state(); got != state {
		h.t.Fatalf("state: want %s, got %s", state, got)
	}
	if tk.Status != status {
		h.t.Fatalf("status: want %s, got %s", status, tk.Status)
	}
	if tk.AssigneeID != assignee {
		h.t.Fatalf("assignee: want %q, got %q", assignee, tk.AssigneeID)
	}
}

func TestAChangeGoesFromImplementToMerged(t *testing.T) {
	h := newHarness(t)
	h.pass()
	h.expect("implement", "in_progress", "agent-impl")

	h.reply("agent-impl", "done")
	h.pass()
	h.expect("review", "in_progress", "agent-rev")

	h.reply("agent-rev", "approved")
	h.pass()
	h.expect("approve", "in_review", "")

	h.board.Move("1", "done")
	h.pass()
	h.expect("merged", "done", "")
}

func TestEachPassMakesAtMostOneTransition(t *testing.T) {
	h := newHarness(t)
	h.pass()
	h.reply("agent-impl", "done")
	h.pass()
	h.pass()
	h.expect("review", "in_progress", "agent-rev")
}

func TestEnteringAnAgentStateStartsExactlyOneRun(t *testing.T) {
	h := newHarness(t)
	h.pass()
	h.pass()
	if h.board.Runs["1"] != 1 {
		t.Fatalf("want 1 run, got %d", h.board.Runs["1"])
	}
}

func TestRequestedChangesGoBackToTheImplementer(t *testing.T) {
	h := newHarness(t)
	h.pass()
	h.reply("agent-impl", "done")
	h.pass()
	h.reply("agent-rev", "changes_requested")
	h.pass()
	h.expect("implement", "in_progress", "agent-impl")
}

func TestTheOldResultDoesNotCountAgainAfterReturning(t *testing.T) {
	h := newHarness(t)
	h.pass()
	h.reply("agent-impl", "done")
	h.pass()
	h.reply("agent-rev", "changes_requested")
	h.pass()
	h.pass() // the implementer's first "done" is before this entry
	h.expect("implement", "in_progress", "agent-impl")
}

func TestAnOutcomeTheStateDoesNotAcceptCountsAsFailed(t *testing.T) {
	h := newHarness(t)
	h.pass()
	h.reply("agent-impl", "approved")
	h.pass()
	h.expect("stuck", "in_review", "")
}

func TestAResultFromAnotherAuthorIsIgnored(t *testing.T) {
	h := newHarness(t)
	h.pass()
	h.reply("agent-rev", "done")
	h.pass()
	h.expect("implement", "in_progress", "agent-impl")
}

func TestARefusedBoardMoveIsPutBack(t *testing.T) {
	h := newHarness(t)
	h.pass()
	h.reply("agent-impl", "failed")
	h.pass()
	h.expect("stuck", "in_review", "")

	h.board.Move("1", "done") // stuck accepts only todo and cancelled
	h.pass()
	h.expect("stuck", "in_review", "")
	comments, _ := h.board.Comments("1")
	if last := comments[len(comments)-1].Content; !strings.Contains(last, "Accepted moves: cancelled, todo") {
		t.Fatalf("want the accepted moves in the comment, got %q", last)
	}
}

func TestAStatusChangedDuringAnAgentStateIsPutBack(t *testing.T) {
	h := newHarness(t)
	h.pass()
	h.board.Move("1", "done")
	h.pass()
	h.expect("implement", "in_progress", "agent-impl")
}

func TestAnUnassignedAgentIsAssignedAgain(t *testing.T) {
	h := newHarness(t)
	h.pass()
	_ = h.board.Unassign("1")
	h.pass()
	h.expect("implement", "in_progress", "agent-impl")
}

func TestCancellingEndsTheWorkflow(t *testing.T) {
	h := newHarness(t)
	h.pass()
	h.reply("agent-impl", "failed")
	h.pass()
	h.board.Move("1", "cancelled")
	h.pass()
	h.expect("abandoned", "cancelled", "")
}

func TestTicketsWithoutAWorkflowAreLeftAlone(t *testing.T) {
	h := newHarness(t)
	h.board.Add(tracker.Ticket{ID: "2", Identifier: "WS-2", Status: "todo"})
	h.pass()
	if tk := h.board.Get("2"); tk.Status != "todo" || tk.AssigneeID != "" || tk.Metadata[engine.KeyLog] != nil {
		t.Fatalf("a ticket without a workflow was changed: %+v", tk)
	}
}

func TestOneBrokenTicketDoesNotStopTheOthers(t *testing.T) {
	h := newHarness(t)
	h.board.Add(tracker.Ticket{ID: "0", Identifier: "WS-0", Status: "todo", Metadata: map[string]any{engine.KeyWorkflow: "missing"}})
	h.pass()
	h.expect("implement", "in_progress", "agent-impl")
}

func TestAMissingRoleChangesNothing(t *testing.T) {
	h := newHarness(t)
	h.eng.Board = tracker.NewMemory(map[string]string{"reviewer": "agent-rev"})
	board := h.eng.Board.(*tracker.Memory)
	board.Add(tracker.Ticket{ID: "1", Identifier: "WS-1", Status: "todo", Metadata: map[string]any{engine.KeyWorkflow: "change"}})
	h.pass()
	if tk := board.Get("1"); tk.Status != "todo" || tk.Metadata[engine.KeyLog] != nil {
		t.Fatalf("want the ticket untouched, got %+v", tk)
	}
}

func TestParseResult(t *testing.T) {
	cases := map[string]struct {
		content string
		ok      bool
		outcome string
	}{
		"no block":         {"Done.", false, ""},
		"a valid block":    {"```workflow-result\n{\"outcome\": \"Done\"}\n```", true, "done"},
		"the last block":   {"```workflow-result\n{\"outcome\": \"failed\"}\n```\n```workflow-result\n{\"outcome\": \"done\"}\n```", true, "done"},
		"invalid JSON":     {"```workflow-result\n{outcome: done}\n```", true, "failed"},
		"not an event":     {"```workflow-result\n{\"outcome\": \"all good!\"}\n```", true, "failed"},
		"another language": {"```json\n{\"outcome\": \"done\"}\n```", false, ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r, ok := engine.ParseResult(c.content)
			if ok != c.ok || r.Outcome != c.outcome {
				t.Fatalf("want (%q, %v), got (%q, %v)", c.outcome, c.ok, r.Outcome, ok)
			}
		})
	}
}

func TestTheLogIsReadFromDecodedJSONToo(t *testing.T) {
	tk := tracker.Ticket{Metadata: map[string]any{engine.KeyLog: []any{map[string]any{"event": "start", "to": "implement", "seen": 1.0}}}}
	log, err := engine.ReadLog(tk)
	if err != nil || len(log) != 1 || log[0].To != "implement" || log[0].Seen != 1 {
		t.Fatalf("got %+v, %v", log, err)
	}
}
