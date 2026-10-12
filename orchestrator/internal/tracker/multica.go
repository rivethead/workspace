package tracker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Multica implements Tracker with the `multica` CLI, signed in as the
// orchestrator's own identity. Tokens stay with the CLI; this code never sees
// them.
type Multica struct {
	WorkspaceID string
}

const pageSize = 100

func (c Multica) run(args ...string) ([]byte, error) {
	full := append([]string{"--workspace-id", c.WorkspaceID}, args...)
	cmd := exec.Command("multica", full...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("multica %s: %s", strings.Join(args, " "), strings.TrimSpace(stderr.String()))
	}
	return bytes.TrimSpace(stdout.Bytes()), nil
}

func (c Multica) Tickets() ([]Ticket, error) {
	type row struct {
		ID         string         `json:"id"`
		Identifier string         `json:"identifier"`
		Title      string         `json:"title"`
		Status     string         `json:"status"`
		AssigneeID *string        `json:"assignee_id"`
		Metadata   map[string]any `json:"metadata"`
	}
	var out []Ticket
	for offset := 0; ; offset += pageSize {
		data, err := c.run("issue", "list", "--limit", strconv.Itoa(pageSize), "--offset", strconv.Itoa(offset), "--output", "json")
		if err != nil {
			return nil, err
		}
		var page struct {
			Issues []row `json:"issues"`
		}
		if err := json.Unmarshal(data, &page); err != nil {
			return nil, fmt.Errorf("decoding the issue list: %w", err)
		}
		for _, r := range page.Issues {
			t := Ticket{ID: r.ID, Identifier: r.Identifier, Title: r.Title, Status: r.Status, Metadata: r.Metadata}
			if r.AssigneeID != nil {
				t.AssigneeID = *r.AssigneeID
			}
			if t.Metadata == nil {
				t.Metadata = map[string]any{}
			}
			out = append(out, t)
		}
		if len(page.Issues) < pageSize {
			return out, nil
		}
	}
}

func (c Multica) Agents() (map[string]string, error) {
	data, err := c.run("agent", "list", "--output", "json")
	if err != nil {
		return nil, err
	}
	var rows []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, fmt.Errorf("decoding the agent list: %w", err)
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.Name] = r.ID
	}
	return out, nil
}

func (c Multica) Comments(id string) ([]Comment, error) {
	data, err := c.run("issue", "comment", "list", id, "--output", "json")
	if err != nil {
		return nil, err
	}
	var rows []struct {
		AuthorType string `json:"author_type"`
		AuthorID   string `json:"author_id"`
		Content    string `json:"content"`
	}
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, fmt.Errorf("decoding the comments of %s: %w", id, err)
	}
	out := make([]Comment, len(rows))
	for i, r := range rows {
		out[i] = Comment{AuthorType: r.AuthorType, AuthorID: r.AuthorID, Content: r.Content}
	}
	return out, nil
}

func (c Multica) AddComment(id, text string) error {
	_, err := c.run("issue", "comment", "add", id, "--content", text)
	return err
}

func (c Multica) Assign(id, agentID string) error {
	_, err := c.run("issue", "assign", id, "--to-id", agentID)
	return err
}

func (c Multica) Unassign(id string) error {
	_, err := c.run("issue", "assign", id, "--unassign")
	return err
}

func (c Multica) SetStatus(id, status string) error {
	_, err := c.run("issue", "status", id, status, "--no-start")
	return err
}

func (c Multica) SetMetadata(id, key, value string) error {
	_, err := c.run("issue", "metadata", "set", id, "--key", key, "--value", value)
	return err
}
