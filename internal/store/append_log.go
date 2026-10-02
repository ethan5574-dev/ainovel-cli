package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
)

// appendLog stores append-only facts as JSONL. Caller must hold the io.mu write lock.
//
// Initial loading builds an in-memory deduplication index; ordinary appends write only new records. Legacy JSON arrays
// migrate on first append, deleting the old file only after saving JSONL, so interruption remains replayable.
type appendLog[T any] struct {
	path       string
	legacyPath string
	key        func(T) string
	clone      func(T) T

	loaded        bool
	hasLog        bool
	legacyPresent bool
	values        []T
	seen          map[string]struct{}
}

func newAppendLog[T any](path, legacyPath string, key func(T) string, clone func(T) T) *appendLog[T] {
	return &appendLog[T]{path: path, legacyPath: legacyPath, key: key, clone: clone}
}

func (l *appendLog[T]) loadUnlocked(io *IO) error {
	if l.loaded {
		return nil
	}
	l.reset()

	data, err := committedJSONLinesUnlocked(io, l.path)
	switch {
	case err == nil:
		values, err := decodeJSONLines[T](l.path, data)
		if err != nil {
			return err
		}
		l.hasLog = true
		l.setValues(values)
	case os.IsNotExist(err):
		var legacy []T
		if err := io.ReadJSONUnlocked(l.legacyPath, &legacy); err != nil {
			if !os.IsNotExist(err) {
				return err
			}
		} else {
			l.legacyPresent = true
		}
		l.setValues(legacy)
	default:
		return err
	}

	if !l.legacyPresent {
		if _, err := os.Stat(io.path(l.legacyPath)); err == nil {
			l.legacyPresent = true
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	l.loaded = true
	return nil
}

func (l *appendLog[T]) allUnlocked(io *IO) ([]T, error) {
	if err := l.loadUnlocked(io); err != nil {
		return nil, err
	}
	return l.cloneValues(l.values), nil
}

// appendUnlocked returns actual new records, including committed JSONL records when legacy-file cleanup fails.
// Callers can mark derived projections for repair; a subsequent replay only cleans up.
func (l *appendLog[T]) appendUnlocked(io *IO, incoming []T) ([]T, error) {
	if err := l.loadUnlocked(io); err != nil {
		return nil, err
	}

	added := make([]T, 0, len(incoming))
	pending := make(map[string]struct{}, len(incoming))
	for _, value := range incoming {
		key := l.key(value)
		if _, ok := l.seen[key]; ok {
			continue
		}
		if _, ok := pending[key]; ok {
			continue
		}
		pending[key] = struct{}{}
		added = append(added, l.clone(value))
	}

	if !l.hasLog && (l.legacyPresent || len(added) > 0) {
		all := append(l.cloneValues(l.values), l.cloneValues(added)...)
		data, err := encodeJSONLines(all)
		if err != nil {
			return nil, err
		}
		if err := io.WriteFileUnlocked(l.path, data); err != nil {
			l.reset()
			return nil, err
		}
		l.hasLog = true
	} else if len(added) > 0 {
		data, err := encodeJSONLines(added)
		if err != nil {
			return nil, err
		}
		if err := io.AppendLineUnlocked(l.path, data); err != nil {
			// A failed write may leave an unterminated tail. Drop the cache so the next load
			// explicitly truncates the uncommitted tail under the commit protocol before replay.
			l.reset()
			return nil, err
		}
	} else if len(incoming) > 0 && l.hasLog {
		// An earlier append may have written full records but failed Sync.
		// Idempotent replay resynchronizes before reporting success; readable does not imply durable.
		if err := io.syncFileUnlocked(l.path); err != nil {
			l.reset()
			return nil, err
		}
	}

	for _, value := range added {
		cloned := l.clone(value)
		l.values = append(l.values, cloned)
		l.seen[l.key(cloned)] = struct{}{}
	}

	if l.hasLog && l.legacyPresent {
		if err := io.RemoveFileUnlocked(l.legacyPath); err != nil {
			return l.cloneValues(added), err
		}
		l.legacyPresent = false
		slog.Info("Growing facts migrated to append-only logs",
			"module", "store", "from", l.legacyPath, "to", l.path, "records", len(l.values))
	}
	return l.cloneValues(added), nil
}

func (l *appendLog[T]) replaceUnlocked(io *IO, values []T) error {
	data, err := encodeJSONLines(values)
	if err != nil {
		return err
	}
	if err := io.WriteFileUnlocked(l.path, data); err != nil {
		l.reset()
		return err
	}
	l.hasLog = true
	l.setValues(values)
	l.loaded = true
	if err := io.RemoveFileUnlocked(l.legacyPath); err != nil {
		l.legacyPresent = true
		return err
	}
	l.legacyPresent = false
	return nil
}

func (l *appendLog[T]) setValues(values []T) {
	l.values = l.cloneValues(values)
	l.seen = make(map[string]struct{}, len(values))
	for _, value := range l.values {
		l.seen[l.key(value)] = struct{}{}
	}
}

func (l *appendLog[T]) cloneValues(values []T) []T {
	if len(values) == 0 {
		return nil
	}
	cloned := make([]T, len(values))
	for i, value := range values {
		cloned[i] = l.clone(value)
	}
	return cloned
}

func (l *appendLog[T]) reset() {
	l.loaded = false
	l.hasLog = false
	l.legacyPresent = false
	l.values = nil
	l.seen = nil
}

func encodeJSONLines[T any](values []T) ([]byte, error) {
	var data bytes.Buffer
	for i, value := range values {
		line, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("encode jsonl record %d: %w", i+1, err)
		}
		data.Write(line)
		data.WriteByte('\n')
	}
	return data.Bytes(), nil
}

func decodeJSONLines[T any](path string, data []byte) ([]T, error) {
	lines := bytes.Split(data, []byte{'\n'})
	values := make([]T, 0, len(lines))
	for i, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var value T
		if err := json.Unmarshal(line, &value); err != nil {
			return nil, fmt.Errorf("parse %s line %d: %w", path, i+1, err)
		}
		values = append(values, value)
	}
	return values, nil
}

// committedJSONLinesUnlocked drops only provably uncommitted tails: a JSONL record is committed
// only when newline-terminated. Corrupt complete lines still return errors; no speculative repair.
func committedJSONLinesUnlocked(io *IO, path string) ([]byte, error) {
	data, err := io.ReadFileUnlocked(path)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || data[len(data)-1] == '\n' {
		return data, nil
	}
	keep := bytes.LastIndexByte(data, '\n') + 1
	if err := os.Truncate(io.path(path), int64(keep)); err != nil {
		return nil, err
	}
	slog.Warn("Repaired uncommitted append-log tail",
		"module", "store", "file", path, "discarded_bytes", len(data)-keep)
	return data[:keep], nil
}
