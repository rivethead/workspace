package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func write(t *testing.T, dir, name, content string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestInlineConfigReplacesMulticasFilesWithTheirContents(t *testing.T) {
	tmp := t.TempDir()
	mcp := write(t, filepath.Join(tmp, "multica-mcp-123"), "mcp-config.json", `{"mcpServers":{}}`)
	settings := write(t, filepath.Join(tmp, "ws", "ws-1"), "claude-runtime-skill-settings.json", `{"skillOverrides":{}}`)
	got, err := inlineConfig([]string{"-p", "--mcp-config=" + mcp, "--settings", settings})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-p", "--mcp-config", `{"mcpServers":{}}`, "--settings", `{"skillOverrides":{}}`}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestInlineConfigReadsNoOtherFile(t *testing.T) {
	tmp := t.TempDir()
	secret := write(t, filepath.Join(tmp, ".multica"), "config.json", `{"token":"mul_runner"}`)
	for _, args := range [][]string{
		{"-p", "--mcp-config", secret},
		{"-p", "--settings", secret},
		{"-p", "--mcp-config", "relative/multica-mcp-1/mcp-config.json"},
		{"-p", "--model", "--mcp-config=" + secret},
	} {
		if got, err := inlineConfig(args); err == nil {
			t.Errorf("inlineConfig(%q) = %q, want an error", args, got)
		}
	}
}
