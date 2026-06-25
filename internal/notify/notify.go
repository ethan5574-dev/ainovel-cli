// Package notify provides unattended notification alerts.
//
// Alignment (architecture.md §2.3): Pure observation layer action—alerts never intervene in control flow
// (no retries, no redirection, no shutdown), they just broadcast existing TUI events externally.
// Send runs asynchronously, never blocks the Host, and logs errors to slog.
package notify

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Notification represents all factual data of a single alert.
type Notification struct {
	Kind  string `json:"kind"`  // run_end / repeat / budget
	Level string `json:"level"` // info / warn / error
	Title string `json:"title"`
	Body  string `json:"body"`
}

// Notifier distributes notifications based on configuration. Zero value is unusable, must be created via New; nil-safe (Send is a no-op).
type Notifier struct {
	command string          // When non-empty, replaces the system channel (e.g., mobile push goes here)
	events  map[string]bool // nil = allow all kinds
	timeout time.Duration
}

// New creates a Notifier. Empty command runs on the built-in system channel (macOS osascript /
// Linux notify-send, falls back silently to slog if command is missing); non-empty events whitelist only the listed kinds.
func New(command string, events []string) *Notifier {
	n := &Notifier{command: strings.TrimSpace(command), timeout: 10 * time.Second}
	if len(events) > 0 {
		n.events = make(map[string]bool, len(events))
		for _, ev := range events {
			n.events[ev] = true
		}
	}
	return n
}

// Send asynchronously sends a notification. Filtering, execution, and error handling do not block the caller.
func (n *Notifier) Send(nt Notification) {
	if !n.allows(nt.Kind) {
		return
	}
	go n.deliver(nt)
}

// allows reports whether the kind is allowed (intercepts if Notifier is nil or kind is not whitelisted).
func (n *Notifier) allows(kind string) bool {
	if n == nil {
		return false
	}
	return n.events == nil || n.events[kind]
}

// deliver synchronously executes a transmission (runs in a goroutine; tests can invoke this directly to assert synchronously).
func (n *Notifier) deliver(nt Notification) {
	defer func() { recover() }()
	ctx, cancel := context.WithTimeout(context.Background(), n.timeout)
	defer cancel()

	var err error
	if n.command != "" {
		err = runCommand(ctx, n.command, nt)
	} else {
		err = runSystem(ctx, nt)
	}
	if err != nil {
		slog.Warn("notification delivery failed", "module", "notify", "kind", nt.Kind, "err", err)
	}
}

// runCommand runs a user-configured command: fields are passed via environment variables (zero dependency,
// no injection risk), and the full JSON payload is piped to stdin. Timeouts are enforced by ctx.
func runCommand(ctx context.Context, command string, nt Notification) error {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Env = append(os.Environ(),
		"NOTIFY_KIND="+nt.Kind,
		"NOTIFY_LEVEL="+nt.Level,
		"NOTIFY_TITLE="+nt.Title,
		"NOTIFY_BODY="+nt.Body,
	)
	payload, _ := json.Marshal(nt)
	cmd.Stdin = strings.NewReader(string(payload))
	return cmd.Run()
}

// runSystem executes built-in desktop alerts: covers only interactive environments, degrades to logging if command is missing.
func runSystem(ctx context.Context, nt Notification) error {
	switch runtime.GOOS {
	case "darwin":
		script := "display notification " + appleScriptString(nt.Body) + " with title " + appleScriptString(nt.Title)
		return exec.CommandContext(ctx, "osascript", "-e", script).Run()
	case "linux":
		if _, err := exec.LookPath("notify-send"); err != nil {
			slog.Info("notification downgraded to log (missing notify-send)", "module", "notify", "title", nt.Title, "body", nt.Body)
			return nil
		}
		return exec.CommandContext(ctx, "notify-send", nt.Title, nt.Body).Run()
	default:
		slog.Info("notification downgraded to log (no system channel on platform)", "module", "notify", "title", nt.Title, "body", nt.Body)
		return nil
	}
}

// appleScriptString wraps arbitrary text into an AppleScript string literal.
func appleScriptString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}
