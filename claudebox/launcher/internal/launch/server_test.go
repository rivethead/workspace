package launch_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/example-org/workspace/claudebox/launcher/internal/launch"
	"github.com/example-org/workspace/claudebox/launcher/internal/wire"
)

type checker struct{ refuse *wire.Refusal }

func (c checker) Check(context.Context, string) error {
	if c.refuse != nil {
		return *c.refuse
	}
	return nil
}

// echoRunner stands in for docker: it echoes stdin to stdout, says its name on
// stderr, and exits 7.
type echoRunner struct {
	block chan struct{}
}

func (e echoRunner) Run(ctx context.Context, name string, _, _ []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	if e.block != nil {
		<-ctx.Done()
		close(e.block)
		return 130, ctx.Err()
	}
	_, _ = io.Copy(stdout, stdin)
	_, _ = io.WriteString(stderr, name)
	return 7, nil
}

func serve(t *testing.T, c launch.Checker, r launch.Runner) net.Conn {
	t.Helper()
	s := &launch.Server{Config: cfg, Checker: c, Runner: r, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	client, srv := net.Pipe()
	go s.Handle(context.Background(), srv)
	t.Cleanup(func() { client.Close() })
	return client
}

// collect reads frames until the last one.
func collect(t *testing.T, conn net.Conn) (stdout, stderr string, code int, refusal *wire.Refusal) {
	t.Helper()
	for {
		typ, data, err := wire.ReadFrame(conn)
		if err != nil {
			t.Fatalf("read frame: %v", err)
		}
		switch typ {
		case wire.Stdout:
			stdout += string(data)
		case wire.Stderr:
			stderr += string(data)
		case wire.Exit:
			code, _ = wire.ExitCode(data)
			return
		case wire.Refused:
			var r wire.Refusal
			_ = json.Unmarshal(data, &r)
			return stdout, stderr, 0, &r
		}
	}
}

func TestARunStreamsBothWaysAndReturnsTheExitCode(t *testing.T) {
	conn := serve(t, checker{}, echoRunner{})
	go func() {
		_ = wire.WriteRequest(conn, request())
		w := wire.NewWriter(conn)
		_ = w.Frame(wire.Stdin, []byte(`{"type":"user"}`))
		_ = w.Frame(wire.StdinClose, nil)
	}()
	stdout, stderr, code, refusal := collect(t, conn)
	if refusal != nil {
		t.Fatalf("refused: %v", refusal)
	}
	if stdout != `{"type":"user"}` || !strings.HasPrefix(stderr, "run-ada-") || code != 7 {
		t.Fatalf("stdout=%q stderr=%q code=%d", stdout, stderr, code)
	}
}

func TestARefusalFromTheBrokerStartsNoRun(t *testing.T) {
	ran := false
	r := runnerFunc(func() { ran = true })
	conn := serve(t, checker{refuse: &wire.Refusal{Reason: "wrong_placement", Detail: "x"}}, r)
	go func() { _ = wire.WriteRequest(conn, request()) }()
	_, _, _, refusal := collect(t, conn)
	if refusal == nil || refusal.Reason != "wrong_placement" {
		t.Fatalf("refusal = %v", refusal)
	}
	if ran {
		t.Fatal("a container was started after the broker refused")
	}
}

func TestABadRequestIsRefusedBeforeTheBrokerIsAsked(t *testing.T) {
	asked := false
	conn := serve(t, checkerFunc(func() { asked = true }), runnerFunc(func() {}))
	req := request()
	req.Workdir = "/etc"
	go func() { _ = wire.WriteRequest(conn, req) }()
	_, _, _, refusal := collect(t, conn)
	if refusal == nil || refusal.Reason != "bad_workdir" || asked {
		t.Fatalf("refusal = %v, broker asked = %v", refusal, asked)
	}
}

func TestTheRunIsStoppedWhenTheWrapperGoesAway(t *testing.T) {
	stopped := make(chan struct{})
	conn := serve(t, checker{}, echoRunner{block: stopped})
	_ = wire.WriteRequest(conn, request())
	time.Sleep(50 * time.Millisecond)
	conn.Close()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("the run kept going after the wrapper disconnected")
	}
}

type runnerFunc func()

func (f runnerFunc) Run(context.Context, string, []string, []string, io.Reader, io.Writer, io.Writer) (int, error) {
	f()
	return 0, nil
}

type checkerFunc func()

func (f checkerFunc) Check(context.Context, string) error { f(); return nil }

func TestALargeRequestStartsARun(t *testing.T) {
	conn := serve(t, checker{}, echoRunner{})
	req := request()
	req.Args = append(req.Args, "--mcp-config", `{"mcpServers":{"big":{"type":"http","url":"http://broker:8443/`+strings.Repeat("x", 100<<10)+`"}}}`)
	go func() {
		_ = wire.WriteRequest(conn, req)
		_ = wire.NewWriter(conn).Frame(wire.StdinClose, nil)
	}()
	_, _, code, refusal := collect(t, conn)
	if refusal != nil || code != 7 {
		t.Fatalf("refusal = %v, code = %d", refusal, code)
	}
}

// fakeDocker writes a docker CLI that prints its arguments and exits 3
// without reading stdin.
func fakeDocker(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "docker")
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho \"$@\"\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDockerRunnerReturnsWhenTheRunEndsThoughStdinStaysOpen(t *testing.T) {
	stdin, _ := io.Pipe() // never closed, as when Claude exits before the daemon closes stdin
	var out strings.Builder
	start := time.Now()
	code, err := launch.DockerRunner{Docker: fakeDocker(t)}.Run(t.Context(), "run-x", []string{"run"}, nil, stdin, &out, io.Discard)
	if err != nil || code != 3 {
		t.Fatalf("code = %d, err = %v", code, err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("the runner waited %v for stdin after the run ended", time.Since(start))
	}
}

func TestDockerRunnerPassesValuesThroughTheEnvironment(t *testing.T) {
	p := filepath.Join(t.TempDir(), "docker")
	_ = os.WriteFile(p, []byte("#!/bin/sh\necho \"$MULTICA_TOKEN $CLAUDE_CODE_OAUTH_TOKEN\"\n"), 0o755)
	var out strings.Builder
	_, err := launch.DockerRunner{Docker: p, ClaudeToken: "sk-ant-oat-x"}.Run(t.Context(), "run-x", nil, []string{"MULTICA_TOKEN=mat_t"}, strings.NewReader(""), &out, io.Discard)
	if err != nil || strings.TrimSpace(out.String()) != "mat_t sk-ant-oat-x" {
		t.Fatalf("out = %q, err = %v", out.String(), err)
	}
}

func TestTheRunIsStoppedWhenTheWrapperGoesAwayAfterClosingStdin(t *testing.T) {
	stopped := make(chan struct{})
	conn := serve(t, checker{}, echoRunner{block: stopped})
	w := wire.NewWriter(conn)
	_ = wire.WriteRequest(conn, request())
	_ = w.Frame(wire.StdinClose, nil)
	_ = w.Frame(wire.Stdin, []byte("late"))
	time.Sleep(50 * time.Millisecond)
	conn.Close()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("the run kept going after the wrapper disconnected")
	}
}

func TestACancelledRunIsRemovedAfterTheCLIExits(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	docker := filepath.Join(dir, "docker")
	_ = os.WriteFile(docker, []byte("#!/bin/sh\necho \"$1\" >> "+log+"\n[ \"$1\" = run ] && sleep 5\nexit 0\n"), 0o755)
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(100*time.Millisecond, cancel)
	code, _ := launch.DockerRunner{Docker: docker}.Run(ctx, "run-x", []string{"run"}, nil, strings.NewReader(""), io.Discard, io.Discard)
	calls, _ := os.ReadFile(log)
	if code != 130 || strings.Count(string(calls), "rm") < 2 {
		t.Fatalf("code = %d, docker calls:\n%s", code, calls)
	}
}
