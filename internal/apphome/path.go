// Package apphome resolves the shared directory used for user-level Ainovel data.
package apphome

import (
	"os"
	"path/filepath"
	"strings"
)

const envName = "AINOVEL_HOME"

// Dir returns the shared data directory. AINOVEL_HOME overrides the platform home
// directory; otherwise data stays in ~/.ainovel.
func Dir() string {
	if dir := strings.TrimSpace(os.Getenv(envName)); dir != "" {
		return filepath.Clean(dir)
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".ainovel")
}
