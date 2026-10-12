package launch

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/example-org/workspace/claudebox/launcher/internal/wire"
)

// Checker asks the authority outside the laptop whether a run may start (D50).
type Checker interface {
	Check(ctx context.Context, agent string) error
}

// Runner runs one container and streams its standard streams. env holds the
// values of the variables that args pass by name.
type Runner interface {
	Run(ctx context.Context, name string, args, env []string, stdin io.Reader, stdout, stderr io.Writer) (int, error)
}

type Server struct {
	Config  Config
	Checker Checker
	Runner  Runner
	Log     *slog.Logger
}

// Serve accepts wrapper connections until ctx ends.
func (s *Server) Serve(ctx context.Context, l net.Listener) error {
	go func() { <-ctx.Done(); l.Close() }()
	for {
		conn, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go s.Handle(ctx, conn)
	}
}

// Handle serves one run. If the wrapper goes away (the daemon cancelled the
// task), the run's container is removed.
func (s *Server) Handle(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	out := wire.NewWriter(conn)

	req, err := wire.ReadRequest(conn)
	if err != nil {
		_ = out.Refuse(wire.Refusal{Reason: "bad_request", Detail: err.Error()})
		return
	}
	run, err := s.check(ctx, req)
	if err != nil {
		var r wire.Refusal
		if !errors.As(err, &r) {
			r = wire.Refusal{Reason: "launcher_error", Detail: err.Error()}
		}
		s.Log.Info("refused", "agent", req.Agent, "task", req.TaskID, "reason", r.Reason, "detail", r.Detail)
		_ = out.Refuse(r)
		return
	}
	stdinR, stdinW := io.Pipe()
	go func() {
		// Wrapper -> container stdin. Keep reading after stdin closes: a read
		// error means the wrapper is gone, and the run must stop.
		for {
			t, data, err := wire.ReadFrame(conn)
			if err != nil {
				stdinW.CloseWithError(err)
				cancel()
				return
			}
			switch t {
			case wire.Stdin:
				_, _ = stdinW.Write(data) // fails only once stdin is closed
			case wire.StdinClose:
				stdinW.Close()
			}
		}
	}()

	s.Log.Info("started", "agent", run.Agent, "task", run.TaskID, "container", "run-"+run.ID)
	started := time.Now()
	code, err := s.Runner.Run(ctx, "run-"+run.ID, DockerArgs(s.Config, run), RunEnv(run), stdinR, out.Stream(wire.Stdout), out.Stream(wire.Stderr))
	if err != nil {
		s.Log.Error("run failed", "agent", run.Agent, "task", run.TaskID, "err", err)
		_, _ = fmt.Fprintf(out.Stream(wire.Stderr), "[launcher] run failed: %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	s.Log.Info("finished", "agent", run.Agent, "task", run.TaskID, "exit", code, "seconds", int(time.Since(started).Seconds()))
	_ = out.Exit(code)
}

// check checks the request here, then asks the broker.
func (s *Server) check(ctx context.Context, req wire.Request) (Run, error) {
	run, err := Check(s.Config, req)
	if err != nil {
		return Run{}, err
	}
	return run, s.Checker.Check(ctx, run.Agent)
}

// BrokerChecker asks the credential service's launcher check through agentgateway,
// with the launcher's own JWT-SVID.
type BrokerChecker struct {
	URL    string // http://broker:8443
	Token  func(ctx context.Context) (string, error)
	Client *http.Client
}

func (b BrokerChecker) Check(ctx context.Context, agent string) error {
	tok, err := b.Token(ctx)
	if err != nil {
		return wire.Refusal{Reason: "no_identity", Detail: err.Error()}
	}
	body, _ := json.Marshal(map[string]string{"agent": agent})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.URL+"/v1/launcher/check", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.Client.Do(req)
	if err != nil {
		return wire.Refusal{Reason: "broker_unreachable", Detail: err.Error()}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var answer struct {
		Allowed bool   `json:"allowed"`
		Reason  string `json:"reason"`
		Detail  string `json:"detail"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		return wire.Refusal{Reason: "broker_error", Detail: fmt.Sprintf("%s: %s", resp.Status, bytes.TrimSpace(raw))}
	}
	if resp.StatusCode != http.StatusOK || !answer.Allowed {
		return wire.Refusal{Reason: answer.Reason, Detail: answer.Detail}
	}
	return nil
}

// SVIDToken fetches a JWT-SVID for audience from the SPIRE agent with its CLI.
func SVIDToken(socket, audience string) func(ctx context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		out, err := exec.CommandContext(ctx, "spire-agent", "api", "fetch", "jwt", "-audience", audience, "-socketPath", socket).Output()
		if err != nil {
			return "", fmt.Errorf("fetch JWT-SVID: %w", err)
		}
		// Output: "token(spiffe://...):\n\t<jwt>\n..."
		lines := bytes.Split(out, []byte("\n"))
		for i, l := range lines {
			if bytes.HasPrefix(l, []byte("token(")) && i+1 < len(lines) {
				return string(bytes.TrimSpace(lines[i+1])), nil
			}
		}
		return "", errors.New("no JWT-SVID in spire-agent output")
	}
}

// DockerRunner runs the docker CLI. The sponsor's token and the run's
// variables reach docker through its environment only (docker run -e NAME),
// never the command line.
type DockerRunner struct {
	ClaudeToken string
	Docker      string // the docker CLI; "docker" when empty
}

func (d DockerRunner) Run(ctx context.Context, name string, args, env []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	docker := cmp.Or(d.Docker, "docker")
	// docker reads stdin from a pipe the launcher feeds, not from stdin itself:
	// otherwise Wait would wait for stdin to close after the run has ended.
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		return 0, err
	}
	go func() {
		_, _ = io.Copy(stdinW, stdin)
		stdinW.Close()
	}()
	defer stdinW.Close()

	cmd := exec.CommandContext(ctx, docker, args...)
	cmd.Env = append(append(os.Environ(), "CLAUDE_CODE_OAUTH_TOKEN="+d.ClaudeToken), env...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdinR, stdout, stderr
	// Killing the docker CLI would leave the container running; remove it.
	cmd.Cancel = func() error { return exec.Command(docker, "rm", "-f", name).Run() }
	cmd.WaitDelay = 30 * time.Second
	err = cmd.Start()
	stdinR.Close()
	if err != nil {
		return 0, err
	}
	err = cmd.Wait()
	if ctx.Err() != nil {
		// The cancel may have come before docker created the container.
		_ = exec.Command(docker, "rm", "-f", name).Run()
		return 130, ctx.Err()
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	return 0, err
}
