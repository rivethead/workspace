# Agent workspace

How AI agents can become working members of an organisation: each agent has its own identity, role, team and human sponsor, gets only short-lived credentials, and a person can stop it in one minute or less. This repo holds the design, the agent runtime, the credential broker, a simplified orchestrator and a local prototype that runs the whole workspace on one computer.

- [Architecture](ARCHITECTURE.md): the vision, the principles and the design: the layers (registry, identity, policy, access, communication, work, execution, oversight), the threats, the decisions and the open questions (why).
- [Infrastructure plan](docs/plan.md): what we deploy in a cloud account, and in which stages (what and how).
- [Threat model](docs/threat-model.md): the threats to the design with STRIDE (security) and LINDDUN (privacy), each with a status and a test.
- [Onboarding](docs/onboarding.md): run the full workspace on your own computer (macOS or Windows 11).
- [Tutorial](docs/tutorial.md): how the parts work together, one lesson at a time.

| Folder | Contents |
|---|---|
| [`claudebox/`](claudebox/) | The agent runtime: the ClaudeBox image, the run wrapper and the launcher |
| [`broker/`](broker/) | The credential broker: the stop switch, the launcher check and GitHub tokens |
| [`orchestrator/`](orchestrator/) | A simplified Multica orchestrator that moves tickets through workflows |
| `local/` | The local prototype: the workspace on one computer, with local stand-ins for the cloud services and simulated laptops |

This is a shared, anonymised copy. Names, domains and accounts are examples: `example.com`, `example-org` and the sponsor "Alex" stand in for the real ones. The Terraform for the cloud account (`infrastructure/`) is not part of this copy, and `orchestrator/` is a simplified version of the real orchestrator. The agent registry is a separate repo, `example-org/agent-registry`, which is not public.
