package host

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/voocel/ainovel-cli/internal/store"
)

func TestUpgradeProjectMigratesEnglishLegacyPremise(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SavePremise("# The Last Light\n\n## Core Conflict\n\nAn archivist must choose between truth and survival.\n\n## Protagonist Goal\n\nFind the missing record."); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.Dir(), "meta", "progress.json"), []byte(`{"novel_name":"The Last Light"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := upgradeProject(st); err != nil {
		t.Fatalf("English legacy migration: %v", err)
	}
	book, err := st.Book.Load()
	if err != nil {
		t.Fatal(err)
	}
	if book == nil || book.Title != "The Last Light" || book.Synopsis != "An archivist must choose between truth and survival." {
		t.Fatalf("migrated book = %+v", book)
	}
}
