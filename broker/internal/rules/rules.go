// Package rules holds what each agent may do, as the provisioner publishes it
// from the registry (ARCHITECTURE.md D37: broker rules are data). The broker
// only reads them.
package rules

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// Statuses an agent can have in the registry. Only an active agent gets anything.
const (
	StatusActive    = "active"
	StatusSuspended = "suspended"
	StatusRetired   = "retired"
)

type Set struct {
	Agents map[string]Agent `json:"agents"`
}

type Agent struct {
	Status  string `json:"status"`
	Sponsor string `json:"sponsor"`
	// Placement is the laptop (<person>.<machine>) the agent runs on (D49).
	Placement string `json:"placement"`
	GitHub    GitHub `json:"github"`
}

func (a Agent) Active() bool { return a.Status == StatusActive }

// GitHub is the agent's own GitHub App and the most its tokens may cover.
type GitHub struct {
	AppID          int64 `json:"app_id"`
	InstallationID int64 `json:"installation_id"`
	// Key names where the App's private key is: "file:<path>" locally,
	// "kms:<key id or alias>" in the cloud.
	Key          string            `json:"key"`
	Repositories []string          `json:"repositories"`
	Permissions  map[string]string `json:"permissions"`
}

// Load reads and checks a rules file.
func Load(path string) (Set, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Set{}, fmt.Errorf("read rules: %w", err)
	}
	return parse(path, raw)
}

func parse(path string, raw []byte) (Set, error) {
	var s Set
	if err := json.Unmarshal(raw, &s); err != nil {
		return Set{}, fmt.Errorf("parse rules %s: %w", path, err)
	}
	for name, a := range s.Agents {
		switch a.Status {
		case StatusActive, StatusSuspended, StatusRetired:
		default:
			return Set{}, fmt.Errorf("rules %s: agent %s has unknown status %q", path, name, a.Status)
		}
		if a.Placement == "" {
			return Set{}, fmt.Errorf("rules %s: agent %s has no placement", path, name)
		}
		// GitHub reads a token request without permissions or repositories as
		// "everything the App has", so both must be listed.
		if a.GitHub.AppID != 0 && (len(a.GitHub.Permissions) == 0 || len(a.GitHub.Repositories) == 0) {
			return Set{}, fmt.Errorf("rules %s: agent %s has a GitHub App but no permissions or no repositories", path, name)
		}
	}
	return s, nil
}

// File serves the rules in a file and reads it again on every call, so the
// provisioner can publish new rules while the broker runs. It parses the file
// only when its content changes; the file is small.
type File struct {
	path string

	mu  sync.Mutex
	raw []byte
	set Set
	err error
}

// OpenFile loads path once; it fails if the first load fails.
func OpenFile(path string) (*File, error) {
	f := &File{path: path}
	if _, err := f.Current(); err != nil {
		return nil, err
	}
	return f, nil
}

// Current returns the rules in the file. If the file is missing or does not
// load, it returns the error until the file is fixed: the broker then refuses
// everything rather than keep rules that may have been meant to suspend an
// agent.
func (f *File) Current() (Set, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	raw, err := os.ReadFile(f.path)
	if err != nil {
		f.raw, f.err = nil, fmt.Errorf("read rules: %w", err)
		return Set{}, f.err
	}
	if f.raw == nil || !bytes.Equal(raw, f.raw) {
		f.raw = raw
		f.set, f.err = parse(f.path, raw)
	}
	return f.set, f.err
}
