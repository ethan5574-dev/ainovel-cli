package worker

import (
	"fmt"
	"os"
	"path/filepath"
)

// ChapterStore persists a finished chapter's text somewhere the web app can serve it
// from, and returns the key SaveChapter should record. Swappable: LocalChapterStore is
// the MVP stand-in the plan calls "chương hoàn thành lên storage" — a real object-storage
// (S3/R2) implementation drops in later behind the same interface, no caller changes.
type ChapterStore interface {
	Save(novelID string, chapter int, content string) (storageKey string, err error)
}

// LocalChapterStore writes chapter text under root/<novelID>/<chapter>.md. Fine for a
// single-worker MVP; not safe to point two independent deployments at the same root.
type LocalChapterStore struct {
	root string
}

func NewLocalChapterStore(root string) *LocalChapterStore {
	return &LocalChapterStore{root: root}
}

func (l *LocalChapterStore) Save(novelID string, chapter int, content string) (string, error) {
	dir := filepath.Join(l.root, novelID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	key := filepath.Join(novelID, fmt.Sprintf("%02d.md", chapter))
	if err := os.WriteFile(filepath.Join(l.root, key), []byte(content), 0o644); err != nil {
		return "", err
	}
	return key, nil
}
