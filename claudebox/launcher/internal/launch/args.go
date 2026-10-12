package launch

import (
	"encoding/json"
	"slices"
	"strings"
)

// claudeOptions are the options Multica 0.6.1 passes to Claude
// (server/pkg/agent/claude.go, buildClaudeArgs), and whether each takes a
// value. A run accepts no others: options such as --add-dir, --plugin-dir or
// --dangerously-skip-permissions would reach past the box's fixed setup.
var claudeOptions = map[string]bool{
	"-p":                  false,
	"--verbose":           false,
	"--strict-mcp-config": false,
	"--output-format":     true,
	"--input-format":      true,
	"--permission-mode":   true,
	"--disallowedTools":   true,
	"--model":             true,
	"--effort":            true,
	"--max-turns":         true,
	"--resume":            true,
	"--mcp-config":        true, // inlined by the wrapper: JSON, not a path
	"--settings":          true, // inlined by the wrapper: JSON, not a path
}

// Option is one parsed Claude option; Value is empty for a flag.
type Option struct {
	Name, Value string
}

// ParseArgs splits Claude's arguments into options, accepting only
// claudeOptions. A value never starts with "-", so an option cannot hide in
// another option's value (say, a model named "--mcp-config=...").
func ParseArgs(args []string) ([]Option, error) {
	var opts []Option
	for i := 0; i < len(args); i++ {
		name, value, inline := strings.Cut(args[i], "=")
		takesValue, known := claudeOptions[name]
		if !known {
			return nil, refuse("bad_args", "%q is not an option a run accepts", args[i])
		}
		switch {
		case !takesValue && inline:
			return nil, refuse("bad_args", "%s takes no value", name)
		case takesValue && !inline:
			if i+1 >= len(args) {
				return nil, refuse("bad_args", "%s needs a value", name)
			}
			i++
			value = args[i]
		}
		if takesValue && (value == "" || strings.HasPrefix(value, "-")) {
			return nil, refuse("bad_args", "%s has no value or a value that looks like an option: %q", name, value)
		}
		opts = append(opts, Option{Name: name, Value: value})
	}
	return opts, nil
}

// FormatArgs is the inverse of ParseArgs.
func FormatArgs(opts []Option) []string {
	args := make([]string, 0, 2*len(opts))
	for _, o := range opts {
		args = append(args, o.Name)
		if claudeOptions[o.Name] {
			args = append(args, o.Value)
		}
	}
	return args
}

// checkInlineConfig accepts the JSON Multica writes for --mcp-config (only
// remote "mcpServers") and for --settings (only its skill policy:
// "skillOverrides" and permissions.deny).
func checkInlineConfig(o Option) error {
	allowed := map[string][]string{
		"--mcp-config": {"mcpServers"},
		"--settings":   {"skillOverrides", "permissions"},
	}[o.Name]
	if allowed == nil {
		return nil
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(o.Value), &top); err != nil {
		return refuse("bad_args", "%s must be inline JSON: %v", o.Name, err)
	}
	for k := range top {
		if !slices.Contains(allowed, k) {
			return refuse("bad_args", "%s may not set %q", o.Name, k)
		}
	}
	if raw, ok := top["mcpServers"]; ok {
		if err := checkRemoteServers(raw); err != nil {
			return err
		}
	}
	if raw, ok := top["permissions"]; ok {
		var perms map[string]json.RawMessage
		if err := json.Unmarshal(raw, &perms); err != nil {
			return refuse("bad_args", "--settings permissions: %v", err)
		}
		for k := range perms {
			if k != "deny" {
				return refuse("bad_args", "--settings may only deny permissions, not set %q", k)
			}
		}
	}
	return nil
}

// checkArgs parses a request's arguments, checks inline configuration and
// adds --strict-mcp-config, so a run sees only the MCP servers it was given.
func checkArgs(args []string) ([]string, error) {
	opts, err := ParseArgs(args)
	if err != nil {
		return nil, err
	}
	strict := false
	for _, o := range opts {
		if err := checkInlineConfig(o); err != nil {
			return nil, err
		}
		strict = strict || o.Name == "--strict-mcp-config"
	}
	if !strict {
		opts = append(opts, Option{Name: "--strict-mcp-config"})
	}
	return FormatArgs(opts), nil
}

// checkRemoteServers accepts only remote MCP servers (a URL; ARCHITECTURE.md
// D20). A local server is a command the run would start before any OpenAPPA
// hook sees it, and anyone who can edit the agent in Multica chooses it.
func checkRemoteServers(raw json.RawMessage) error {
	var servers map[string]struct {
		Type    string          `json:"type"`
		URL     string          `json:"url"`
		Command json.RawMessage `json:"command"`
	}
	if err := json.Unmarshal(raw, &servers); err != nil {
		return refuse("bad_args", "--mcp-config mcpServers: %v", err)
	}
	for name, s := range servers {
		if s.Command != nil || (s.Type != "http" && s.Type != "sse") || s.URL == "" {
			return refuse("bad_args", "MCP server %q must be a remote server (type http or sse, with a url)", name)
		}
	}
	return nil
}
