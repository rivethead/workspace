// Command claudebox-launcher starts every agent run in a fresh container
// (ARCHITECTURE.md D14, 5.7).
//
//	claudebox-launcher serve        the launcher: the only part with Docker access
//	claudebox-launcher run <args>   the run wrapper, at MULTICA_CLAUDE_PATH in the daemon
//
// The wrapper answers the daemon's `--version` probe itself and sends every
// other call to the launcher, streaming stdin, stdout and stderr both ways.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/example-org/workspace/claudebox/launcher/internal/launch"
	"github.com/example-org/workspace/claudebox/launcher/internal/wire"
)

const defaultSocket = "/run/launcher/launcher.sock"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: claudebox-launcher serve | run <claude args>")
		os.Exit(2)
	}
	var err error
	code := 0
	switch os.Args[1] {
	case "serve":
		err = serve()
	case "run":
		code, err = wrap(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "[launcher]", err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

func env(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func serve() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	cfg := launch.Config{
		Laptop:        os.Getenv("LAUNCHER_LAPTOP"),
		Project:       os.Getenv("LAUNCHER_PROJECT"),
		Image:         os.Getenv("LAUNCHER_IMAGE"),
		Networks:      strings.Split(os.Getenv("LAUNCHER_NETWORKS"), ","),
		WorkspaceRoot: env("LAUNCHER_WORKSPACE_ROOT", "/home/claude/multica-workspaces"),
	}
	// The sponsor's Team seat token (D15) reaches runs through docker's
	// environment only; it is never logged.
	claudeToken := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN")
	for name, v := range map[string]string{"LAUNCHER_LAPTOP": cfg.Laptop, "LAUNCHER_PROJECT": cfg.Project, "LAUNCHER_IMAGE": cfg.Image} {
		if v == "" {
			return fmt.Errorf("%s must be set", name)
		}
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if claudeToken == "" {
		log.Warn("CLAUDE_CODE_OAUTH_TOKEN is not set: runs will start but cannot call Claude")
	}
	socket := env("LAUNCHER_SOCKET", defaultSocket)
	_ = os.Remove(socket)
	l, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	// Only the daemon's container mounts the socket's volume.
	if err := os.Chmod(socket, 0o666); err != nil {
		return err
	}
	s := &launch.Server{
		Config: cfg,
		Checker: launch.BrokerChecker{
			URL:    env("LAUNCHER_BROKER_URL", "http://broker:8443"),
			Token:  launch.SVIDToken(env("LAUNCHER_SPIRE_SOCKET", "/run/spire/sockets/agent.sock"), "broker"),
			Client: &http.Client{Timeout: 15 * time.Second},
		},
		Runner: launch.DockerRunner{ClaudeToken: claudeToken},
		Log:    log,
	}
	log.Info("listening", "socket", socket, "laptop", cfg.Laptop, "image", cfg.Image)
	return s.Serve(ctx, l)
}

// wrap is the run wrapper. It returns the run's exit code.
func wrap(args []string) (int, error) {
	if len(args) == 1 && (args[0] == "--version" || args[0] == "-v") {
		return localClaude(args)
	}
	workdir, err := os.Getwd()
	if err != nil {
		return 1, err
	}
	args, err = inlineConfig(args)
	if err != nil {
		return 1, err
	}
	req := wire.Request{
		Agent: os.Getenv("MULTICA_AGENT_NAME"), TaskID: os.Getenv("MULTICA_TASK_ID"),
		Workdir: workdir, Args: args, Env: map[string]string{},
	}
	for _, k := range launch.TaskEnv {
		if v, ok := os.LookupEnv(k); ok {
			req.Env[k] = v
		}
	}
	conn, err := net.Dial("unix", env("CLAUDEBOX_LAUNCHER_SOCKET", defaultSocket))
	if err != nil {
		return 1, fmt.Errorf("reach the launcher: %w", err)
	}
	defer conn.Close()
	if err := wire.WriteRequest(conn, req); err != nil {
		return 1, err
	}
	go func() {
		w := wire.NewWriter(conn)
		_, _ = io.Copy(w.Stream(wire.Stdin), os.Stdin)
		_ = w.Frame(wire.StdinClose, nil)
	}()
	for {
		t, data, err := wire.ReadFrame(conn)
		if err != nil {
			return 1, fmt.Errorf("launcher connection: %w", err)
		}
		switch t {
		case wire.Stdout:
			_, _ = os.Stdout.Write(data)
		case wire.Stderr:
			_, _ = os.Stderr.Write(data)
		case wire.Exit:
			return wire.ExitCode(data)
		case wire.Refused:
			var r wire.Refusal
			_ = json.Unmarshal(data, &r)
			return 126, fmt.Errorf("run refused: %s: %s", r.Reason, r.Detail)
		}
	}
}

// configFiles are the files Multica writes for --mcp-config and --settings
// (server/pkg/agent/claude.go, server/internal/daemon/execenv): the run cannot
// see them, so the wrapper sends their contents. It reads no other file, so a
// changed argument cannot make it send one of the daemon's own files.
var configFiles = map[string]func(dir, base string) bool{
	"--mcp-config": func(dir, base string) bool {
		return strings.HasPrefix(filepath.Base(dir), "multica-mcp-") && base == "mcp-config.json"
	},
	"--settings": func(_, base string) bool { return base == "claude-runtime-skill-settings.json" },
}

// inlineConfig replaces the --mcp-config and --settings files with their
// contents: Claude Code accepts the JSON itself.
func inlineConfig(args []string) ([]string, error) {
	opts, err := launch.ParseArgs(args)
	if err != nil {
		return nil, err
	}
	for i, o := range opts {
		allowed, ok := configFiles[o.Name]
		if !ok {
			continue
		}
		file := filepath.Clean(o.Value)
		if !filepath.IsAbs(file) || !allowed(filepath.Dir(file), filepath.Base(file)) {
			return nil, fmt.Errorf("%s must be the file Multica writes, not %q", o.Name, o.Value)
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", o.Name, err)
		}
		opts[i].Value = string(raw)
	}
	return launch.FormatArgs(opts), nil
}

// localClaude runs the box's own claude for the daemon's version probe.
func localClaude(args []string) (int, error) {
	// The entrypoint sets CLAUDEBOX_REAL_CLAUDE to OpenAPPA's clappa.
	bin := env("CLAUDEBOX_REAL_CLAUDE", os.ExpandEnv("$HOME/.local/bin/clappa"))
	if _, err := os.Stat(bin); err != nil {
		return 1, fmt.Errorf("no claude to answer the version probe: %w", err)
	}
	return 0, syscall.Exec(bin, append([]string{bin}, args...), os.Environ())
}
