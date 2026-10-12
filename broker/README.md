# broker

The credential broker: our services behind agentgateway (ARCHITECTURE.md 5.4).

- **Credential service** (`cmd/credential-service`): checks each caller's JWT-SVID itself (audience `broker`, D42), and then:
  - `POST /v1/launcher/check` answers a laptop's launcher before it starts a run: is the agent active, not stopped, and placed on that laptop (D50)?
  - `POST /v1/github/token` mints a one-hour token with the agent's own GitHub App, for one repository in its rules and only the permissions they list.
  - `credential-service stop <agent> <by> <reason>` and `start <agent> <by>` set and clear the stop switch (D35). The service reads it on every request. `history <agent>` shows who stopped and started the agent, and when: each stop and start is a row that is never changed (docs/threat-model.md, TM-R1).
  - Every refusal returns a reason code (`stopped`, `wrong_placement`, `repository_not_allowed`, ...), and every decision is logged.
- **Assignment service** (stage 4) and **mail tools** (stage 8): not started.

The rules come from the registry through the provisioner (D37), as a JSON file. The service reads it on every request and refuses everything while it is missing or does not load; `GET /healthz` says why. An agent with a GitHub App must list its repositories and permissions, because GitHub reads a token request without them as everything the App has. GitHub App keys are a signer: a key file locally, KMS in the cloud.

A token covers repositories by name within the App's installation, so the owner in `owner/name` in the rules must be the account the App is installed on; the service does not check this yet.

The deployment is in [docs/plan.md](../docs/plan.md#credential-broker). People make releases by hand (plan D48). To run it locally, see `local/`.
