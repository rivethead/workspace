# orchestrator (simplified)

This is a small, simplified version of the orchestrator that the architecture describes ([ARCHITECTURE.md, 6. Work](../ARCHITECTURE.md#56-work-what-agents-work-on)). It shows the core idea in about 900 lines of Go: a deterministic program moves tickets on a [Multica](https://github.com/multica-ai/multica) board through workflows. Agents do the work, and the orchestrator decides what happens next. No language model is in the loop, and it spends no tokens.

The full orchestrator is not part of this copy. It also judges results against checks on a commit, waits for CI, opens and merges pull requests, runs sprints, and sets projects up. This version keeps only the part that the rest of the workspace depends on: who may start an agent run, and where a ticket's state lives.

## What it guarantees

- A ticket's state is its event log, in the ticket's `statechart` metadata. Nothing else holds state, so the orchestrator can stop at any point and continue where it stopped.
- Only the orchestrator starts an agent run, by assigning the ticket to the role's agent. Outside an agent state the ticket is unassigned, so a stray comment wakes nobody.
- A status change never starts a run (`--no-start`). The orchestrator writes the task before it assigns the agent, and never comments while an agent holds the ticket, so the assignment is what starts the run.
- An agent's result counts only if the assigned agent wrote it, after the task. An outcome that the state does not accept counts as `failed`.
- The orchestrator owns the ticket's status and assignment. It puts back a status that someone changed by hand, unless it is a move that the current human state accepts.
- Each pass moves a ticket by at most one transition. An error on one ticket skips that ticket for the pass, and never stops the others.

## Workflows

A workflow is a YAML file with `schema: workflow/v1`. Each state has a kind:

| Kind | Who acts | What happens |
|---|---|---|
| `agent` | one agent role | On entry, the orchestrator writes the task on the ticket and assigns the role's agent. The agent ends its run with a fenced `workflow-result` block, for example `{"outcome": "done"}`. The outcome is the event. Every agent state handles `failed`. |
| `human` | a person | On entry, the ticket is unassigned and moves to `in_review`, with a comment that says what is ready. The person moves the ticket on the board, and the status they pick is the event (`todo`, `in_progress`, `done`, `cancelled` or `blocked`). |
| `final` | nobody | The workflow is over. The ticket ends in `done`, or in the state's `status`. |

[examples/workflows/change.yaml](examples/workflows/change.yaml) is a full example: an implementer writes a change, a reviewer reviews it, and a person approves it. A person starts a workflow on a ticket by setting its `workflow` metadata:

```bash
multica issue metadata set <ticket> --key workflow --value change
```

The agents of the workspace must have the names of the roles (`implementer`, `reviewer`).

## Run it

You need the `multica` CLI, signed in as the identity that the orchestrator acts as, and Go 1.26 or later.

```bash
go run . validate examples/workflows/*.yaml
go run . run --workspace-id <id> --workflows examples/workflows --once
go run . run --workspace-id <id> --workflows examples/workflows      # one pass every 20 seconds
```

The orchestrator reads the workflows again on every pass. An invalid file stops that pass, not the process.

## Layout

| Path | What it is |
|---|---|
| `main.go` | The `run` and `validate` commands |
| `internal/workflow/` | Loads and validates workflow files |
| `internal/engine/` | The engine: the event log, one step per ticket, and the agents' results |
| `internal/tracker/` | The board: the `Tracker` interface, the `multica` CLI adapter and an in-memory board for tests |
| `examples/workflows/` | The example workflow |

## Checks

```bash
go vet ./... && go test ./... && gofmt -l .
```

The tests run whole workflows pass by pass against the in-memory board, and never call Multica.
