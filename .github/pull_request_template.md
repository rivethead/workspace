## Summary

What changed and why. If a person or an agent could notice the change (a check, a permission, a deploy, a workflow), say what they will see.

## Ticket

<KEY>-<n>, or "none"

## Checklist

- [ ] The checks for each folder this touches pass (see `CLAUDE.md`)
- [ ] `ARCHITECTURE.md` or `docs/plan.md` is updated if this changes a decision, a stage or a deployed resource
- [ ] An ADR is added or updated if this makes an architectural decision
- [ ] If this widens access (broker code, cloud access rules, CI, hooks, CODEOWNERS), the description says so, and a person who did not write it reviews it

### orchestrator/

- [ ] `go vet ./...`, `go test ./...` and `gofmt -l .` are clean
- [ ] New or changed engine behaviour has a flow test that fails without the change
- [ ] The README is updated if a workflow could notice the change
