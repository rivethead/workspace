// Package launch turns a run request into one fresh container, and nothing else
// (ARCHITECTURE.md 5.7: "the launcher is narrow"). The request chooses only the
// agent, the task's work folder, Claude's options and Multica's task
// variables; every Docker option is fixed here.
package launch

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/example-org/workspace/claudebox/launcher/internal/wire"
)

// Config is fixed when the launcher starts; a request cannot change it.
type Config struct {
	Laptop        string   // <person>.<machine>, the launcher's own identity
	Project       string   // the laptop's compose project, the prefix of its volumes
	Image         string   // the pinned ClaudeBox image
	Networks      []string // networks a run joins
	WorkspaceRoot string   // where the daemon keeps work folders
}

// Run is a checked request: what the launcher will start.
type Run struct {
	ID      string // unique per run; the container is run-<ID>
	Agent   string
	TaskID  string
	Workdir string
	Rel     string // Workdir relative to WorkspaceRoot
	Args    []string
	Env     map[string]string
}

var (
	agentName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)
	uuid      = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	segment   = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)
)

// TaskEnv lists the variables Multica sets for a task that a run may get. The
// daemon's own variables (its runner token among them) never reach a run, and
// neither does an agent's custom environment in Multica: a name such as
// ANTHROPIC_BASE_URL could send the sponsor's token elsewhere. The provisioner
// will own an agent's environment (ARCHITECTURE.md D37).
var TaskEnv = []string{
	"MULTICA_TOKEN", "MULTICA_SERVER_URL", "MULTICA_WORKSPACE_ID",
	"MULTICA_AGENT_NAME", "MULTICA_AGENT_ID", "MULTICA_TASK_ID", "MULTICA_TASK_SLOT",
	"MULTICA_AUTOPILOT_ID", "MULTICA_AUTOPILOT_RUN_ID",
	"MULTICA_QUICK_CREATE_TASK_ID", "MULTICA_QUICK_CREATE_ATTACHMENT_IDS",
}

func refuse(reason, format string, args ...any) error {
	return wire.Refusal{Reason: reason, Detail: fmt.Sprintf(format, args...)}
}

// Check validates a request against cfg and returns the Run to start.
func Check(cfg Config, req wire.Request) (Run, error) {
	if !agentName.MatchString(req.Agent) {
		return Run{}, refuse("bad_agent", "agent name %q must be a registry name (lower case)", req.Agent)
	}
	if !uuid.MatchString(req.TaskID) {
		return Run{}, refuse("bad_task", "task ID %q is not a Multica task ID", req.TaskID)
	}
	rel, err := workFolder(cfg.WorkspaceRoot, req.Workdir)
	if err != nil {
		return Run{}, err
	}
	// The container's command is the box's claude; a request passes only the
	// options Multica uses.
	args, err := checkArgs(req.Args)
	if err != nil {
		return Run{}, err
	}
	env := map[string]string{}
	for _, k := range TaskEnv {
		if v, ok := req.Env[k]; ok {
			env[k] = v
		}
	}
	// Multica gives each task its own token (mat_...). Anything else is the
	// daemon's token, which must stay with the daemon.
	if tok, ok := env["MULTICA_TOKEN"]; ok && !strings.HasPrefix(tok, "mat_") {
		return Run{}, refuse("bad_env", "MULTICA_TOKEN must be a task-scoped token")
	}
	env["MULTICA_AGENT_NAME"], env["MULTICA_TASK_ID"] = req.Agent, req.TaskID
	return Run{
		ID: req.Agent + "-" + randomHex(4), Agent: req.Agent, TaskID: req.TaskID,
		Workdir: req.Workdir, Rel: rel, Args: args, Env: env,
	}, nil
}

// workFolder checks that workdir is a task's work folder as Multica lays them
// out, <root>/<workspace>/<issue>/workdir, and returns the path below root. It
// cannot tie the folder to the task: Multica gives a follow-up task the folder
// of the task before it, and the request names the task anyway.
func workFolder(root, workdir string) (string, error) {
	bad := refuse("bad_workdir", "work folder %q is not a task's work folder under %s", workdir, root)
	if path.Clean(workdir) != workdir {
		return "", bad
	}
	rel, ok := strings.CutPrefix(workdir, strings.TrimSuffix(root, "/")+"/")
	parts := strings.Split(rel, "/")
	if !ok || len(parts) != 3 || parts[2] != "workdir" || !segment.MatchString(parts[0]) || !segment.MatchString(parts[1]) {
		return "", bad
	}
	return rel, nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// DockerArgs is the complete `docker` command line for a run. Everything but
// the agent, the work folder and the arguments is fixed. Variables are passed
// by name only; their values reach docker through its environment (RunEnv), so
// they never show on a command line.
func DockerArgs(cfg Config, r Run) []string {
	vol := func(name string) string { return cfg.Project + "_" + name }
	args := []string{
		"run", "--rm", "-i",
		"--name", "run-" + r.ID,
		// SPIRE gives the run the agent's identity from these labels.
		"--label", "workspace.agent=" + r.Agent,
		"--label", "workspace.laptop=" + cfg.Laptop,
		"--label", "workspace.task=" + r.TaskID,
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"--pids-limit", "512",
		"--memory", "4g",
		"--cpus", "2",
		// Only the task's own work folder, at the same path the daemon uses, so
		// Claude Code finds the session again on resume.
		"--mount", fmt.Sprintf("type=volume,src=%s,dst=%s,volume-subpath=%s", vol("multica-workspaces"), r.Workdir, r.Rel),
		// The agent's own sessions, so a follow-up task can resume: only this
		// agent's runs mount them. The rest of ~/.claude (settings, hooks) is
		// fresh in every run, so no run can change what the next one runs with.
		// OpenAPPA's policy also comes fresh from the image in every run.
		"--mount", fmt.Sprintf("type=volume,src=%s,dst=/home/claude/.claude/projects", vol("session-"+r.Agent)),
		"--mount", fmt.Sprintf("type=volume,src=%s,dst=/run/spire/sockets,readonly", vol("spire-agent-socket")),
		"-w", r.Workdir,
		// Multica passes its own permission mode; the box must not add another.
		"-e", "CLAUDEBOX_SKIP_PERMISSIONS=0",
		"-e", "CLAUDE_CODE_OAUTH_TOKEN",
	}
	for _, n := range cfg.Networks {
		args = append(args, "--network", n)
	}
	for _, k := range sortedKeys(r.Env) {
		args = append(args, "-e", k)
	}
	args = append(args, cfg.Image)
	return append(args, r.Args...)
}

// RunEnv is the run's variables as NAME=value, for docker's environment.
func RunEnv(r Run) []string {
	env := make([]string, 0, len(r.Env))
	for _, k := range sortedKeys(r.Env) {
		env = append(env, k+"="+r.Env[k])
	}
	return env
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
