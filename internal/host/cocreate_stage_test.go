package host

import (
	"context"
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/host/imp"
	"github.com/voocel/ainovel-cli/internal/store"
)

// newFlagTestHost creates a minimal Host, just enough to drive the cocreating state machine and concurrency guard.
// emitEvent uses recover + non-blocking select, buffering the events channel, without requiring a coordinator/observer.
// The running-state branch of PauseForCoCreate calls coordinator.Abort (reusing the verified Esc pause path),
// which is not unit tested here; this only covers non-running-state and flag/guard logic not relying on coordinator.
func newFlagTestHost(lc lifecycle, cocreating bool) *Host {
	return &Host{
		lifecycle:  lc,
		cocreating: cocreating,
		events:     make(chan Event, 16),
	}
}

func TestPauseForCoCreate_NonRunningSetsFlag(t *testing.T) {
	h := newFlagTestHost(lifecycleIdle, false)
	if !h.PauseForCoCreate() {
		t.Fatal("idle state should allow entering stage co-creation")
	}
	if !h.cocreating {
		t.Error("cocreating should be true after entering")
	}
	if h.lifecycle != lifecycleIdle {
		t.Errorf("entering from non-running state should not modify lifecycle, got %s", h.lifecycle)
	}
}

func TestPauseForCoCreate_RejectsCompleted(t *testing.T) {
	h := newFlagTestHost(lifecycleCompleted, false)
	if h.PauseForCoCreate() {
		t.Error("stage co-creation should not be allowed after book completion")
	}
	if h.cocreating {
		t.Error("cocreating flag should not be set after rejection")
	}
}

func TestPauseForCoCreate_RejectsReentrant(t *testing.T) {
	h := newFlagTestHost(lifecyclePaused, true)
	if h.PauseForCoCreate() {
		t.Error("reentry should be rejected if already in co-creation")
	}
}

func TestCancelCoCreate_ClearsFlag(t *testing.T) {
	h := newFlagTestHost(lifecyclePaused, true)
	h.CancelCoCreate()
	if h.cocreating {
		t.Error("cocreating should be cleared after cancellation")
	}
	if h.lifecycle != lifecyclePaused {
		t.Errorf("cancellation should not modify lifecycle, got %s", h.lifecycle)
	}
}

func TestCancelCoCreate_NoopWhenNotCocreating(t *testing.T) {
	h := newFlagTestHost(lifecycleRunning, false)
	h.CancelCoCreate() // should not panic, should not modify status
	if h.cocreating || h.lifecycle != lifecycleRunning {
		t.Error("CancelCoCreate in non-co-creation state should be a no-op")
	}
}

func TestResumeFromCoCreate_RejectsEmptyDraft(t *testing.T) {
	h := newFlagTestHost(lifecyclePaused, true)
	if err := h.ResumeFromCoCreate("   "); err == nil {
		t.Fatal("empty draft should return an error")
	}
	if !h.cocreating {
		t.Error("empty draft returns before clearing flag, cocreating should remain true")
	}
}

func TestResumeFromCoCreate_RejectsWhenNotCocreating(t *testing.T) {
	h := newFlagTestHost(lifecyclePaused, false)
	err := h.ResumeFromCoCreate("## Subsequent Direction\n- Enter second volume")
	if err == nil || !strings.Contains(err.Error(), "not in co-create") {
		t.Fatalf("non-co-creation state should report not in co-create, got %v", err)
	}
}

func TestGuardExclusive(t *testing.T) {
	cases := []struct {
		name       string
		lc         lifecycle
		cocreating bool
		wantErr    string // Empty = expected to pass
	}{
		{"running", lifecycleRunning, false, "running"},
		{"cocreating", lifecyclePaused, true, "stage co-creation"},
		{"idle free", lifecycleIdle, false, ""},
		{"paused free", lifecyclePaused, false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newFlagTestHost(c.lc, c.cocreating)
			err := h.guardExclusive("Import")
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("should pass, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("should contain %q, got %v", c.wantErr, err)
			}
			if !strings.Contains(err.Error(), "Import") {
				t.Errorf("error message should contain action %q, got %v", "Import", err)
			}
		})
	}
}

// TestStageCoCreate_OccupancyBlocksConcurrentEntries verifies that all exclusive entry points are blocked within the co-creation window:
// import/start/resume/continue should all be rejected during cocreating, filling the gap of checking only ==running during paused state.
func TestStageCoCreate_OccupancyBlocksConcurrentEntries(t *testing.T) {
	h := newFlagTestHost(lifecycleIdle, false)
	if !h.PauseForCoCreate() {
		t.Fatal("failed to enter stage co-creation")
	}

	if _, err := h.ImportFrom(context.Background(), imp.Options{}); err == nil {
		t.Error("ImportFrom should be rejected within co-creation window")
	}
	if err := h.StartPrepared("write a new story"); err == nil {
		t.Error("StartPrepared should be rejected within co-creation window")
	}
	if _, err := h.Resume(); err == nil {
		t.Error("Resume should be rejected within co-creation window")
	}
	if err := h.Continue("continue writing"); err == nil {
		t.Error("Continue should be rejected within co-creation window")
	}

	// Occupation released after exiting co-creation (Cancel used here; Resume injection path requires coordinator, verified in integration tests)
	h.CancelCoCreate()
	if h.cocreating {
		t.Fatal("occupation flag should be released after exiting")
	}
}

func TestBuildStoryStateSummary_NilStore(t *testing.T) {
	if got := buildStoryStateSummary(nil); got != "" {
		t.Errorf("nil store should return empty string, got %q", got)
	}
}

func TestBuildStoryStateSummary_Populated(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("Shadowverse", 100); err != nil {
		t.Fatal(err)
	}
	p, _ := st.Progress.Load()
	p.CompletedChapters = []int{1, 2, 3}
	p.TotalWordCount = 12000
	if err := st.Progress.Save(p); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveCompass(domain.StoryCompass{
		EndingDirection: "Protagonist reaches the summit",
		OpenThreads:     []string{"Sect blood feud unresolved"},
		EstimatedScale:  "Estimated 4-6 volumes",
	}); err != nil {
		t.Fatal(err)
	}

	got := buildStoryStateSummary(st)
	for _, want := range []string{"Shadowverse", "Completed 3 chapters", "next chapter is Chapter 4", "Protagonist reaches the summit", "Sect blood feud unresolved", "Estimated 4-6 volumes"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary should contain %q, actual:\n%s", want, got)
		}
	}
}
