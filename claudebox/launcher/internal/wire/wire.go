// Package wire is the protocol between the run wrapper (in the daemon's
// container) and the launcher (the only part with Docker access).
//
// The wrapper sends one request frame (JSON), then frames of stdin. The
// launcher answers with frames of stdout and stderr, then one exit or refused
// frame. A frame is one type byte, a 4-byte big-endian length, and the data.
package wire

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// Frame types.
const (
	Req        byte = 'q' // wrapper -> launcher: JSON Request, first frame
	Stdin      byte = 'i' // wrapper -> launcher
	StdinClose byte = 'c' // wrapper -> launcher: no more stdin
	Stdout     byte = 'o' // launcher -> wrapper
	Stderr     byte = 'e' // launcher -> wrapper
	Exit       byte = 'x' // launcher -> wrapper: 4-byte exit code, last frame
	Refused    byte = 'r' // launcher -> wrapper: JSON Refusal, last frame
)

// MaxFrame bounds one frame, so a peer cannot make the other allocate without limit.
const MaxFrame = 1 << 20

// Request asks the launcher to start one run.
type Request struct {
	Agent   string            `json:"agent"`   // the agent's name in the registry
	TaskID  string            `json:"task_id"` // Multica's task ID
	Workdir string            `json:"workdir"` // the task's work folder (absolute)
	Args    []string          `json:"args"`    // arguments for claude; the wrapper inlines --mcp-config
	Env     map[string]string `json:"env"`     // Multica's task variables
}

// Refusal says why the launcher did not start a run.
type Refusal struct {
	Reason string `json:"reason"`
	Detail string `json:"detail"`
}

func (r Refusal) Error() string { return r.Reason + ": " + r.Detail }

func WriteRequest(w io.Writer, req Request) error {
	b, err := json.Marshal(req)
	if err != nil {
		return err
	}
	return NewWriter(w).Frame(Req, b)
}

func ReadRequest(r io.Reader) (Request, error) {
	t, data, err := ReadFrame(r)
	if err != nil {
		return Request{}, fmt.Errorf("read request: %w", err)
	}
	if t != Req {
		return Request{}, fmt.Errorf("first frame must be a request, not %q", t)
	}
	var req Request
	if err := json.Unmarshal(data, &req); err != nil {
		return Request{}, fmt.Errorf("parse request: %w", err)
	}
	return req, nil
}

// Writer writes frames; it is safe for concurrent use (stdout and stderr
// are copied by different goroutines).
type Writer struct {
	mu sync.Mutex
	w  io.Writer
}

func NewWriter(w io.Writer) *Writer { return &Writer{w: w} }

func (fw *Writer) Frame(t byte, data []byte) error {
	if len(data) > MaxFrame {
		return fmt.Errorf("frame of %d bytes is over the limit", len(data))
	}
	fw.mu.Lock()
	defer fw.mu.Unlock()
	var hdr [5]byte
	hdr[0] = t
	binary.BigEndian.PutUint32(hdr[1:], uint32(len(data)))
	if _, err := fw.w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := fw.w.Write(data)
	return err
}

// Stream returns an io.Writer that sends everything written to it as frames of type t.
func (fw *Writer) Stream(t byte) io.Writer { return streamWriter{fw, t} }

type streamWriter struct {
	fw *Writer
	t  byte
}

func (s streamWriter) Write(p []byte) (int, error) {
	for off := 0; off < len(p); off += MaxFrame {
		end := min(off+MaxFrame, len(p))
		if err := s.fw.Frame(s.t, p[off:end]); err != nil {
			return off, err
		}
	}
	return len(p), nil
}

func (fw *Writer) Exit(code int) error {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], uint32(int32(code)))
	return fw.Frame(Exit, b[:])
}

func (fw *Writer) Refuse(r Refusal) error {
	b, _ := json.Marshal(r)
	return fw.Frame(Refused, b)
}

// ReadFrame reads one frame.
func ReadFrame(r io.Reader) (byte, []byte, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(hdr[1:])
	if n > MaxFrame {
		return 0, nil, fmt.Errorf("frame of %d bytes is over the limit", n)
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(r, data); err != nil {
		return 0, nil, err
	}
	return hdr[0], data, nil
}

// ExitCode decodes an Exit frame.
func ExitCode(data []byte) (int, error) {
	if len(data) != 4 {
		return 0, errors.New("exit frame must be 4 bytes")
	}
	return int(int32(binary.BigEndian.Uint32(data))), nil
}
