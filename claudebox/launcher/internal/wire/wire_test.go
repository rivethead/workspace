package wire_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/example-org/workspace/claudebox/launcher/internal/wire"
)

func TestRequestRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	in := wire.Request{Agent: "ada", TaskID: "t1", Workdir: "/w", Args: []string{"-p", "hi"}, Env: map[string]string{"MULTICA_TASK_ID": "t1"}}
	if err := wire.WriteRequest(&buf, in); err != nil {
		t.Fatal(err)
	}
	buf.WriteString("rest")
	out, err := wire.ReadRequest(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if out.Agent != "ada" || out.Args[1] != "hi" || out.Env["MULTICA_TASK_ID"] != "t1" {
		t.Fatalf("got %+v", out)
	}
	if rest := buf.String(); rest != "rest" {
		t.Fatalf("bytes after the request were lost: %q", rest)
	}
}

func TestALargeRequestRoundTrips(t *testing.T) {
	var buf bytes.Buffer
	prompt := strings.Repeat("x", 200<<10)
	if err := wire.WriteRequest(&buf, wire.Request{Agent: "ada", Args: []string{"--append-system-prompt", prompt}}); err != nil {
		t.Fatal(err)
	}
	out, err := wire.ReadRequest(&buf)
	if err != nil || out.Args[1] != prompt {
		t.Fatalf("a 200 KB request did not round-trip: %v", err)
	}
}

func TestAStdinFrameIsNotARequest(t *testing.T) {
	var buf bytes.Buffer
	_ = wire.NewWriter(&buf).Frame(wire.Stdin, []byte(`{"agent":"ada"}`))
	if _, err := wire.ReadRequest(&buf); err == nil {
		t.Fatal("a stdin frame was read as a request")
	}
}

func TestFramesRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	w := wire.NewWriter(&buf)
	_, _ = w.Stream(wire.Stdout).Write([]byte("hello"))
	_ = w.Exit(-3)

	typ, data, err := wire.ReadFrame(&buf)
	if err != nil || typ != wire.Stdout || string(data) != "hello" {
		t.Fatalf("first frame: %c %q %v", typ, data, err)
	}
	typ, data, _ = wire.ReadFrame(&buf)
	code, err := wire.ExitCode(data)
	if typ != wire.Exit || err != nil || code != -3 {
		t.Fatalf("exit frame: %c %d %v", typ, code, err)
	}
}

func TestLargeWritesAreSplitIntoFrames(t *testing.T) {
	var buf bytes.Buffer
	w := wire.NewWriter(&buf)
	big := strings.Repeat("x", wire.MaxFrame+10)
	if _, err := w.Stream(wire.Stdout).Write([]byte(big)); err != nil {
		t.Fatal(err)
	}
	var got int
	for buf.Len() > 0 {
		_, data, err := wire.ReadFrame(&buf)
		if err != nil {
			t.Fatal(err)
		}
		got += len(data)
	}
	if got != len(big) {
		t.Fatalf("got %d bytes, want %d", got, len(big))
	}
}

func TestReadFrameRefusesAnOversizedFrame(t *testing.T) {
	hdr := []byte{wire.Stdout, 0xff, 0xff, 0xff, 0xff}
	if _, _, err := wire.ReadFrame(bytes.NewReader(hdr)); err == nil {
		t.Fatal("an oversized frame was accepted")
	}
}
