// Command ainovel-worker is the platform's background job runner (plan Phase 1): it
// polls generation_jobs in Postgres and drives each one to completion by calling the
// same engine the CLI/TUI/web modes use (internal/host), just with no terminal or
// browser on the other end. See internal/worker for the actual job logic — this file
// is only environment wiring and the poll loop.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/worker"
)

func main() {
	dsn := requireEnv("DATABASE_URL")
	configPath := requireEnv("AINOVEL_CONFIG") // platform's shared provider credentials (bootstrap.Config JSON)
	workspaceRoot := envOr("WORKSPACE_ROOT", "workspace/platform")
	chapterRoot := envOr("CHAPTER_ROOT", "workspace/chapters")
	workerID := envOr("WORKER_ID", defaultWorkerID())
	pollInterval := envDuration("POLL_INTERVAL", 5*time.Second)
	staleAfter := envDuration("STALE_AFTER", 10*time.Minute)
	// System-wide cost layer 3 (plan's business-model.md): 0 means unlimited, so a
	// single-operator deploy without this set behaves exactly as it did in Phase 1.
	dailyBudgetUSD := envFloat("DAILY_BUDGET_USD", 0)
	// Phase 5's "model tốt hơn cho vai Writer" for tier=paid jobs. Both unset (the
	// default) means no override — paid jobs use the same Writer model as everyone
	// else, since there's no business decision yet on what to actually sell.
	paidWriterOverride := envRoleOverride("PAID_WRITER_PROVIDER", "PAID_WRITER_MODEL")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	baseCfg, err := bootstrap.LoadConfigFile(configPath)
	if err != nil {
		fatalf("load config %s: %v", configPath, err)
	}
	baseCfg.FillDefaults()
	if err := baseCfg.ValidateBase(); err != nil {
		fatalf("invalid config %s: %v", configPath, err)
	}

	store, err := worker.OpenPGStore(ctx, dsn)
	if err != nil {
		fatalf("connect to postgres: %v", err)
	}
	defer func() { _ = store.Close() }()

	chapters := worker.NewLocalChapterStore(chapterRoot)

	slog.Info("ainovel-worker started", "worker_id", workerID, "poll_interval", pollInterval, "stale_after", staleAfter, "daily_budget_usd", dailyBudgetUSD, "paid_writer_override", paidWriterOverride != nil)
	run(ctx, store, baseCfg, chapters, workspaceRoot, workerID, pollInterval, staleAfter, dailyBudgetUSD, paidWriterOverride)
	slog.Info("ainovel-worker stopped")
}

// run polls for work until ctx is cancelled (SIGINT/SIGTERM). A missing queue check
// (RequeueStale) runs on the same cadence as ClaimNextJob so a crashed sibling worker's
// jobs come back within one poll interval of staleAfter, not on a separate timer.
func run(ctx context.Context, store *worker.PGStore, baseCfg bootstrap.Config, chapters worker.ChapterStore, workspaceRoot, workerID string, pollInterval, staleAfter time.Duration, dailyBudgetUSD float64, paidWriterOverride *bootstrap.RoleConfig) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if n, err := store.RequeueStale(staleAfter); err != nil {
				slog.Error("requeue stale jobs failed", "err", err)
			} else if n > 0 {
				slog.Warn("requeued stale jobs", "count", n)
			}

			// Cost layer 3: past the daily cap, leave jobs queued rather than claim and
			// spend — they pick up automatically once spend resets at local midnight
			// (TodaySpendUSD uses the DB's own "now()", so this is a moving window, not
			// a fixed reset job).
			if dailyBudgetUSD > 0 {
				spent, err := store.TodaySpendUSD()
				if err != nil {
					slog.Error("check daily spend failed", "err", err)
					continue
				}
				if spent >= dailyBudgetUSD {
					slog.Warn("daily budget reached; not claiming new jobs this tick", "spent_usd", spent, "daily_budget_usd", dailyBudgetUSD)
					continue
				}
			}

			job, err := store.ClaimNextJob(workerID)
			if err != nil {
				slog.Error("claim job failed", "err", err)
				continue
			}
			if job == nil {
				continue // queue empty; wait for the next tick
			}
			runOne(store, baseCfg, chapters, workspaceRoot, job, paidWriterOverride)
		}
	}
}

func runOne(store *worker.PGStore, baseCfg bootstrap.Config, chapters worker.ChapterStore, workspaceRoot string, job *worker.Job, paidWriterOverride *bootstrap.RoleConfig) {
	if job.WorkspaceDir == "" {
		job.WorkspaceDir = defaultWorkspaceDir(workspaceRoot, job.NovelID)
	}
	slog.Info("job started", "job_id", job.ID, "novel_id", job.NovelID, "tier", job.Tier, "budget_usd", job.BudgetUSD)
	if err := worker.RunJob(job, baseCfg, store, chapters, paidWriterOverride); err != nil {
		slog.Error("job errored", "job_id", job.ID, "err", err)
		if failErr := store.FailJob(job.ID, err.Error()); failErr != nil {
			slog.Error("failed to record job failure", "job_id", job.ID, "err", failErr)
		}
		return
	}
	slog.Info("job finished", "job_id", job.ID)
}

func defaultWorkspaceDir(root, novelID string) string {
	return filepath.Join(root, novelID)
}

func defaultWorkerID() string {
	host, err := os.Hostname()
	if err != nil {
		host = "worker"
	}
	return fmt.Sprintf("%s-%d", host, os.Getpid())
}

func requireEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		fatalf("missing required environment variable %s", key)
	}
	return v
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// envRoleOverride returns nil when either env var is unset — a partial override (only
// provider or only model set) is a config mistake, not a valid "off" state, so it fails
// fast instead of silently running paid jobs on an unintended model.
func envRoleOverride(providerKey, modelKey string) *bootstrap.RoleConfig {
	provider := os.Getenv(providerKey)
	model := os.Getenv(modelKey)
	if provider == "" && model == "" {
		return nil
	}
	if provider == "" || model == "" {
		fatalf("%s and %s must be set together (or both left unset)", providerKey, modelKey)
	}
	return &bootstrap.RoleConfig{Provider: provider, Model: model}
}

func envFloat(key string, fallback float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		fatalf("invalid float for %s: %v", key, err)
	}
	return f
}

func envDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		fatalf("invalid duration for %s: %v", key, err)
	}
	return d
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "ainovel-worker: "+format+"\n", args...)
	os.Exit(1)
}
