package notify

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAllowsFilter(t *testing.T) {
	if New("", nil).allows("repeat") != true {
		t.Error("events should allow all by default")
	}
	n := New("", []string{"run_end", "budget"})
	if !n.allows("run_end") || !n.allows("budget") {
		t.Error("whitelisted kind should be allowed")
	}
	if n.allows("repeat") {
		t.Error("unlisted kind should be blocked")
	}
	var nilN *Notifier
	if nilN.allows("run_end") {
		t.Error("nil Notifier should block everything")
	}
	nilN.Send(Notification{Kind: "run_end"}) // should not panic
}

func TestCommandChannelEnvAndStdin(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, "env.txt")
	jsonFile := filepath.Join(dir, "stdin.json")

	n := New(`echo "$NOTIFY_KIND|$NOTIFY_LEVEL|$NOTIFY_TITLE|$NOTIFY_BODY" > `+envFile+` && cat > `+jsonFile, nil)
	nt := Notification{Kind: "budget", Level: "warn", Title: "ainovel: budget", Body: "spent $8.00"}
	n.deliver(nt) // invoke synchronously for assertions

	env, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("command not executed: %v", err)
	}
	if got := strings.TrimSpace(string(env)); got != "budget|warn|ainovel: budget|spent $8.00" {
		t.Errorf("env mismatch: %q", got)
	}

	raw, err := os.ReadFile(jsonFile)
	if err != nil {
		t.Fatalf("stdin not delivered: %v", err)
	}
	var decoded Notification
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("stdin is not valid JSON: %v", err)
	}
	if decoded != nt {
		t.Errorf("stdin JSON mismatch: %+v", decoded)
	}
}

func TestCommandChannelTimeoutKill(t *testing.T) {
	n := New("sleep 30", nil)
	n.timeout = 200 * time.Millisecond

	start := time.Now()
	n.deliver(Notification{Kind: "run_end"})
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("timed out without being killed, blocked %v", elapsed)
	}
}
