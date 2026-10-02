package version

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

// DefaultRepo is the upstream repository for update checks and self-update.
const DefaultRepo = "voocel/ainovel-cli"

// DefaultCheckInterval is the minimum time between network checks. Anonymous GitHub API access is limited
// to 60 requests/hour; releases occur on a days-long cadence, so more frequent checks add noise without value.
const DefaultCheckInterval = 24 * time.Hour

// CheckOptions describes one check. Callers supply CachePath (usually update-check.json
// in the config directory); this package does not depend on bootstrap, preserving version
// as a leaf dependency.
type CheckOptions struct {
	Repo           string
	CurrentVersion string
	Client         *http.Client
	CachePath      string        // Empty disables persistent cache; each call uses the network.
	MaxAge         time.Duration // Cache lifetime; <=0 uses DefaultCheckInterval.
}

// CheckResult describes a check; Notes carries raw release Markdown.
// Callers sanitize for their output medium before displaying; users decide whether to update.
type CheckResult struct {
	Latest          string
	Current         string
	Notes           string
	UpdateAvailable bool
	FromCache       bool
}

// checkCache is the persisted CachePath structure.
type checkCache struct {
	LastCheck time.Time `json:"last_check"`
	Latest    string    `json:"latest"`
	Notes     string    `json:"notes"`
}

// CheckUpdate queries the latest upstream release and decides whether to suggest updating. It never writes
// a binary; users control whether and when to run ainovel-cli update.
// Cache/network failures return errors; failed cache writes still return the fetched result.
func CheckUpdate(ctx context.Context, opts CheckOptions) (*CheckResult, error) {
	current := Normalize(opts.CurrentVersion)
	if current == "dev" {
		// Local builds lack comparable version semantics; skip to avoid false upgrade notifications.
		return &CheckResult{Current: current}, nil
	}
	repo := strings.TrimSpace(opts.Repo)
	if repo == "" {
		repo = DefaultRepo
	}
	maxAge := opts.MaxAge
	if maxAge <= 0 {
		maxAge = DefaultCheckInterval
	}

	var cacheErr error
	if opts.CachePath != "" {
		c, err := loadCache(opts.CachePath)
		switch {
		case err == nil:
			age := time.Since(c.LastCheck)
			if age < 0 {
				cacheErr = fmt.Errorf("update check cache timestamp is in the future: %s", c.LastCheck.Format(time.RFC3339))
			} else if age <= maxAge {
				result, resultErr := c.result(current)
				if resultErr == nil {
					return result, nil
				}
				cacheErr = fmt.Errorf("validate update check cache: %w", resultErr)
			}
		case errors.Is(err, os.ErrNotExist):
			// No cache on the first check is normal.
		default:
			cacheErr = fmt.Errorf("load update check cache: %w", err)
		}
	}

	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	rel, err := fetchRelease(ctx, client, repo, "latest")
	if err != nil {
		return nil, errors.Join(cacheErr, err)
	}
	if rel.TagName == "" {
		return nil, errors.Join(cacheErr, fmt.Errorf("release lacks tag_name"))
	}

	result, err := newCheckResult(rel.TagName, current, rel.Body, false)
	if err != nil {
		return nil, errors.Join(cacheErr, err)
	}
	if err := writeCache(opts.CachePath, rel); err != nil {
		cacheErr = errors.Join(cacheErr, fmt.Errorf("write update check cache: %w", err))
	}
	// Return a companion cache error for logging while retaining the usable check result.
	return result, cacheErr
}

func newCheckResult(latest, current, notes string, fromCache bool) (*CheckResult, error) {
	updateAvailable, err := isNewer(latest, current)
	if err != nil {
		return nil, err
	}
	return &CheckResult{
		Latest:          latest,
		Current:         current,
		Notes:           notes,
		UpdateAvailable: updateAvailable,
		FromCache:       fromCache,
	}, nil
}

// loadCache reads and validates cache; callers handle missing files, corruption, and missing fields separately.
func loadCache(path string) (*checkCache, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c checkCache
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("decode cache: %w", err)
	}
	if c.Latest == "" || c.LastCheck.IsZero() {
		return nil, fmt.Errorf("cache missing latest or last_check")
	}
	return &c, nil
}

// result converts cached data to a result with FromCache=true.
func (c *checkCache) result(current string) (*CheckResult, error) {
	return newCheckResult(c.Latest, current, c.Notes, true)
}

// writeCache atomically saves a check result, creating its directory if needed.
func writeCache(path string, rel *release) error {
	if path == "" {
		return nil
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	data, err := json.Marshal(checkCache{LastCheck: time.Now(), Latest: rel.TagName, Notes: rel.Body})
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".update-check-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		_ = tmp.Close()
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	committed = true
	return nil
}

// isNewer applies full SemVer rules; malformed versions return explicit errors rather than missing upgrades silently.
func isNewer(latest, current string) (bool, error) {
	latest = Normalize(latest)
	current = Normalize(current)
	if !semver.IsValid(latest) {
		return false, fmt.Errorf("invalid latest version %q", latest)
	}
	if !semver.IsValid(current) {
		return false, fmt.Errorf("invalid current version %q", current)
	}
	return semver.Compare(latest, current) > 0, nil
}
