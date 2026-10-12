package rules_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/example-org/workspace/broker/internal/rules"
)

const valid = `{
  "agents": {
    "ada": {
      "status": "active",
      "sponsor": "alex",
      "placement": "alex.mac",
      "github": {
        "app_id": 101,
        "installation_id": 202,
        "key": "file:ada.pem",
        "repositories": ["example-org/workspace-sandbox"],
        "permissions": {"contents": "write", "pull_requests": "write"}
      }
    },
    "grace": {"status": "suspended", "sponsor": "colleague", "placement": "colleague.mac"}
  }
}`

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "rules.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadReadsAgents(t *testing.T) {
	set, err := rules.Load(write(t, valid))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ada, ok := set.Agents["ada"]
	if !ok || ada.Placement != "alex.mac" || ada.GitHub.InstallationID != 202 {
		t.Fatalf("ada = %+v, %v", ada, ok)
	}
	if !ada.Active() {
		t.Error("ada should be active")
	}
	if grace, _ := set.Agents["grace"]; grace.Active() {
		t.Error("a suspended agent reported active")
	}
	if _, ok := set.Agents["nobody"]; ok {
		t.Error("an unknown agent was found")
	}
}

func TestLoadRefusesUnknownStatus(t *testing.T) {
	if _, err := rules.Load(write(t, `{"agents":{"ada":{"status":"on-holiday","placement":"alex.mac"}}}`)); err == nil {
		t.Fatal("an unknown status was accepted")
	}
}

func TestLoadRefusesAnAgentWithoutPlacement(t *testing.T) {
	if _, err := rules.Load(write(t, `{"agents":{"ada":{"status":"active"}}}`)); err == nil {
		t.Fatal("an agent without a placement was accepted")
	}
}

func TestFileReloadsWhenTheFileChanges(t *testing.T) {
	p := write(t, valid)
	f, err := rules.OpenFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(`{"agents":{"ada":{"status":"retired","placement":"alex.mac"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Second)
	_ = os.Chtimes(p, future, future)
	set, err := f.Current()
	if err != nil {
		t.Fatal(err)
	}
	if ada, _ := set.Agents["ada"]; ada.Active() {
		t.Fatal("rules were not reloaded")
	}
}

// touch writes content to p and moves its modification time forward, so the
// change is seen even within the file system's time resolution.
func touch(t *testing.T, p, content string, ahead time.Duration) {
	t.Helper()
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(ahead)
	_ = os.Chtimes(p, future, future)
}

func TestABrokenRulesFileRefusesUntilItIsFixed(t *testing.T) {
	p := write(t, valid)
	f, _ := rules.OpenFile(p)
	touch(t, p, `not json`, time.Second)
	if _, err := f.Current(); err == nil {
		t.Fatal("a broken rules file kept the old rules in force")
	}
	if _, err := f.Current(); err == nil {
		t.Fatal("a broken rules file was accepted on the second read")
	}
	touch(t, p, valid, 2*time.Second)
	if set, err := f.Current(); err != nil || !set.Agents["ada"].Active() {
		t.Fatalf("the fixed rules were not loaded: %v", err)
	}
}

func TestAMissingRulesFileRefuses(t *testing.T) {
	p := write(t, valid)
	f, _ := rules.OpenFile(p)
	_ = os.Remove(p)
	if _, err := f.Current(); err == nil {
		t.Fatal("a deleted rules file kept the old rules in force")
	}
}

func TestLoadRefusesAGitHubAppWithoutLimits(t *testing.T) {
	for _, gh := range []string{
		`{"app_id":1,"installation_id":2,"repositories":["example-org/workspace-sandbox"]}`,
		`{"app_id":1,"installation_id":2,"permissions":{"contents":"read"}}`,
	} {
		if _, err := rules.Load(write(t, `{"agents":{"ada":{"status":"active","placement":"alex.mac","github":`+gh+`}}}`)); err == nil {
			t.Errorf("a GitHub App without permissions or repositories was accepted: %s", gh)
		}
	}
}

func TestRulesWithTheSameModificationTimeAreStillReloaded(t *testing.T) {
	p := write(t, valid)
	f, _ := rules.OpenFile(p)
	info, _ := os.Stat(p)
	_ = os.WriteFile(p, []byte(`{"agents":{"ada":{"status":"suspended","placement":"alex.mac"}}}`), 0o600)
	_ = os.Chtimes(p, info.ModTime(), info.ModTime())
	if set, err := f.Current(); err != nil || set.Agents["ada"].Active() {
		t.Fatalf("a suspension published with the same modification time was missed: %v", err)
	}
}
