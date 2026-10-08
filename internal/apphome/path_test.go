package apphome

import (
	"path/filepath"
	"testing"
)

func TestDirUsesAINOVELHOME(t *testing.T) {
	want := filepath.Join(t.TempDir(), "ainovel-data")
	t.Setenv(envName, want)
	if got := Dir(); got != want {
		t.Fatalf("Dir() = %q, want %q", got, want)
	}
}

func TestDirDefaultsToUserHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv(envName, " ")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if got, want := Dir(), filepath.Join(home, ".ainovel"); got != want {
		t.Fatalf("Dir() = %q, want %q", got, want)
	}
}
