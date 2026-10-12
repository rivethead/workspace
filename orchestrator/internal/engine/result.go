package engine

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/example-org/workspace/orchestrator/internal/workflow"
)

// ResultFence is the info string of the fenced block an agent ends its run
// with.
const ResultFence = "workflow-result"

// Result is what an agent reports at the end of its run.
type Result struct {
	Outcome string `json:"outcome"`
	Reason  string `json:"reason"`
}

var (
	resultBlock = regexp.MustCompile("(?s)```" + ResultFence + "\\s*\\n(.*?)\\n\\s*```")
	eventName   = regexp.MustCompile(`^[a-z][a-z_]*$`)
)

// ParseResult finds the last result block in a comment. ok is false when the
// comment has none. A block that is not valid JSON, or that names no event,
// comes back as a failed result that says why: an agent's typo costs it the
// attempt, and is never silently ignored.
func ParseResult(content string) (r Result, ok bool) {
	ms := resultBlock.FindAllStringSubmatch(content, -1)
	if len(ms) == 0 {
		return Result{}, false
	}
	body := strings.TrimSpace(ms[len(ms)-1][1])
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		return Result{Outcome: workflow.EventFailed, Reason: "the result block is not valid JSON: " + err.Error()}, true
	}
	r.Outcome = strings.ToLower(strings.TrimSpace(r.Outcome))
	if !eventName.MatchString(r.Outcome) {
		return Result{Outcome: workflow.EventFailed, Reason: `the outcome must be an event name such as "done", not "` + r.Outcome + `"`}, true
	}
	return r, true
}
