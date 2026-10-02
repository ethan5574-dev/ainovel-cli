package worker

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/voocel/ainovel-cli/assets"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/host"
	"github.com/voocel/ainovel-cli/internal/store"
)

// heartbeatInterval also paces how often we poll meta/progress.json for newly
// committed chapters — a book with a 90-minute run should never look stalled for
// more than this long from the platform's side.
const heartbeatInterval = 20 * time.Second

// BuildJobConfig layers one job's isolated output directory and hard budget cap onto
// the platform's shared provider credentials. base carries Provider/Model/Providers/
// Roles/Style — everything that's the same for every job on this worker; only
// OutputDir, Budget, and (for tier=paid) the Writer role model vary per job.
//
// paidWriterOverride is Phase 5's "model tốt hơn cho vai Writer" — nil means the worker
// wasn't configured with one (PAID_WRITER_MODEL unset), so paid jobs fall back to the
// same Writer model as everyone else rather than erroring.
func BuildJobConfig(base bootstrap.Config, job *Job, paidWriterOverride *bootstrap.RoleConfig) bootstrap.Config {
	cfg := base
	cfg.OutputDir = job.WorkspaceDir
	cfg.Budget = bootstrap.BudgetConfig{
		BookUSD:   job.BudgetUSD,
		WarnRatio: 0.8,
		HardStop:  true,
	}
	if job.Tier == "paid" && paidWriterOverride != nil {
		// Roles is a map (reference type): copy it before writing, so this job's
		// override doesn't leak into base.Roles and every other job sharing it.
		roles := make(map[string]bootstrap.RoleConfig, len(base.Roles)+1)
		for k, v := range base.Roles {
			roles[k] = v
		}
		roles["writer"] = *paidWriterOverride
		cfg.Roles = roles
	}
	return cfg
}

// RunJob drives one book to completion (or to a budget/error stop) and reports the
// outcome through jobs. It reuses the engine exactly as headless/run.go does — same
// host.New / Resume / StartPrepared calls — but harvests chapters and usage into
// Postgres instead of writing prose to stdout, since there's no terminal to write to.
//
// The prompt is prepared up front; upstream no longer pauses runs for AskUser input.
func RunJob(job *Job, base bootstrap.Config, jobs JobStore, chapters ChapterStore, paidWriterOverride *bootstrap.RoleConfig) error {
	cfg := BuildJobConfig(base, job, paidWriterOverride)
	if err := os.MkdirAll(cfg.OutputDir, 0o755); err != nil {
		return fmt.Errorf("create workspace dir: %w", err)
	}

	// Deliberately not assets.DefaultLoadOptions: that also layers in the worker
	// process's own ~/.ainovel/style, which on a multi-tenant platform is nobody's
	// book — it would leak whatever style file happens to sit on this host into every
	// user's novel. Only the per-job book directory's style override applies here.
	bundle := assets.Load(cfg.Style, assets.LoadOptions{BookStyleDir: filepath.Join(cfg.OutputDir, "style")})

	eng, err := host.New(cfg, bundle)
	if err != nil {
		return fmt.Errorf("start engine: %w", err)
	}
	defer eng.Close()

	label, err := eng.Resume()
	if err != nil {
		return fmt.Errorf("resume: %w", err)
	}
	if label == "" {
		if err := eng.PrepareUserRules(job.Prompt); err != nil {
			return fmt.Errorf("prepare user rules: %w", err)
		}
		if err := eng.StartPrepared(job.Prompt); err != nil {
			return fmt.Errorf("start: %w", err)
		}
	}

	return consumeJob(eng, job, jobs, chapters)
}

// consumeJob drains the engine's channels until Done() closes, harvesting newly
// completed chapters on a fixed cadence (not per-event: chapter commits aren't a
// distinct event Category, and polling meta/progress.json is the same source of truth
// the eval harness's checkpoint checks use).
func consumeJob(eng *host.Host, job *Job, jobs JobStore, chapters ChapterStore) error {
	st := store.NewStore(eng.Dir())
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	var prevProgress *domain.Progress
	var lastError string
	aborted := false

	harvest := func() error {
		cur, err := st.Progress.Load()
		if err != nil {
			return fmt.Errorf("load progress: %w", err)
		}
		for _, ch := range newlyCompletedChapters(prevProgress, cur) {
			if err := harvestChapter(st, chapters, jobs, job.NovelID, ch); err != nil {
				return err
			}
		}
		prevProgress = cur
		if err := harvestReviews(st, jobs, job.NovelID); err != nil {
			return err
		}
		if err := harvestForeshadows(st, jobs, job.NovelID); err != nil {
			return err
		}
		return nil
	}

	// finish drains to Done() before returning, whatever the trigger (normal
	// completion, chapter-cap abort, error). Returning early here would race
	// eng.Close()'s channel close against the engine's own background completion
	// send — the same send-on-closed-channel hazard internal/eval/runner.go documents.
	for {
		select {
		case ev, ok := <-eng.Events():
			if !ok {
				continue
			}
			if ev.Level == "error" {
				lastError = ev.Summary
			}
			// job.MaxChapters==0 means unset: let the book run to its own outline length.
			if !aborted && job.MaxChapters > 0 && eng.Snapshot().CompletedCount >= job.MaxChapters {
				eng.Abort()
				aborted = true
			}
		case _, ok := <-eng.Stream():
			if !ok {
				continue
			}
			// Streamed prose isn't persisted token-by-token; a whole chapter is
			// harvested once meta/progress.json marks it complete.
		case <-ticker.C:
			if err := jobs.Heartbeat(job.ID); err != nil {
				return fmt.Errorf("heartbeat: %w", err)
			}
			if err := harvest(); err != nil {
				return err
			}
		case _, ok := <-eng.Done():
			if !ok {
				return finishJob(st, job, jobs, chapters, &prevProgress, lastError, aborted)
			}
			return finishJob(st, job, jobs, chapters, &prevProgress, lastError, aborted)
		}
	}
}

// finishJob runs one last harvest pass (the ticker may not have fired between the
// final chapter commit and Done()), records usage, and reports success or failure.
// Success means either the book's own phase reached Complete, or the run was stopped
// deliberately at job.MaxChapters (capAborted) — that stop is the platform's own
// design (e.g. a trial's 5-chapter cap), not a failure. Anything else — an error event
// or a budget hard-stop — is reported as failed with the last error event as reason.
func finishJob(st *store.Store, job *Job, jobs JobStore, chapters ChapterStore, prevProgress **domain.Progress, lastError string, capAborted bool) error {
	cur, err := st.Progress.Load()
	if err != nil {
		return fmt.Errorf("load final progress: %w", err)
	}
	for _, ch := range newlyCompletedChapters(*prevProgress, cur) {
		if err := harvestChapter(st, chapters, jobs, job.NovelID, ch); err != nil {
			return err
		}
	}
	*prevProgress = cur

	if err := harvestReviews(st, jobs, job.NovelID); err != nil {
		return fmt.Errorf("harvest reviews: %w", err)
	}
	if err := harvestForeshadows(st, jobs, job.NovelID); err != nil {
		return fmt.Errorf("harvest foreshadows: %w", err)
	}
	if err := harvestUsage(st, jobs, job.ID, job.NovelID); err != nil {
		return fmt.Errorf("harvest usage: %w", err)
	}

	succeeded := capAborted || (cur != nil && cur.Phase == domain.PhaseComplete)
	if succeeded {
		// Quality gate only runs on success: a failed/budget-stopped run's partial
		// reviews aren't a meaningful signal for whether the book should go public.
		if err := computeQualityGate(st, jobs, job.NovelID); err != nil {
			return fmt.Errorf("compute quality gate: %w", err)
		}
		if err := jobs.UpdateNovelStatus(job.NovelID, "complete"); err != nil {
			return err
		}
		return jobs.CompleteJob(job.ID)
	}

	reason := lastError
	if reason == "" {
		reason = "run ended before the book reached phase=complete (likely a budget stop)"
	}
	if err := jobs.UpdateNovelStatus(job.NovelID, "failed"); err != nil {
		return err
	}
	return jobs.FailJob(job.ID, reason)
}
