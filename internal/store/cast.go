package store

import (
	"os"
	"slices"
	"sort"

	"github.com/voocel/ainovel-cli/internal/domain"
)

// CastStore manages the supporting characters ledger (meta/cast_ledger.json).
//
// Supporting characters ledger records "named minor characters that appeared", orthogonal to characters.json (core character profiles):
//   - characters.json: main + key supporting characters explicitly designed by the Architect, not modified during writing
//   - cast_ledger.json: automatically accumulated by the commit_chapter tool, containing all named non-core supporting characters
//
// MergeAppearances is idempotent: repeatedly committing the same chapter won't duplicate AppearanceCount.
type CastStore struct{ io *IO }

func NewCastStore(io *IO) *CastStore { return &CastStore{io: io} }

const castLedgerPath = "meta/cast_ledger.json"

// Load reads the supporting characters ledger. Returns nil slice when file does not exist.
func (s *CastStore) Load() ([]domain.CastEntry, error) {
	var entries []domain.CastEntry
	if err := s.io.ReadJSON(castLedgerPath, &entries); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return entries, nil
}

// Save saves the supporting characters ledger as a whole (atomic write).
func (s *CastStore) Save(entries []domain.CastEntry) error {
	return s.io.WriteJSON(castLedgerPath, entries)
}

// MergeAppearances merges this chapter's character appearance records into the ledger.
//
// Parameters:
//   - chapter: current chapter number
//   - characters: array of character names appearing in this chapter (from commit_chapter.Characters)
//   - intros: brief new character descriptions declared explicitly by the Writer (first appearance or completing BriefRole)
//   - knownCore: map of core character names already in characters.json (these skip ledger writing)
//
// Behavior:
//   - Name is in knownCore: skip (core character profiles are their sole record entrypoint)
//   - Name is already in ledger and chapter is already in AppearanceChapters: completely skip (idempotent)
//   - Name is already in ledger but chapter is new: update LastSeenChapter + append chapter + count++
//   - Name is not in ledger: add new entry
//   - BriefRole in intros is adopted only when ledger entry's BriefRole is empty, to avoid overwriting earlier descriptions
func (s *CastStore) MergeAppearances(
	chapter int,
	characters []string,
	intros []domain.CastIntro,
	knownCore map[string]bool,
) error {
	if chapter <= 0 || len(characters) == 0 {
		return nil
	}
	return s.io.WithWriteLock(func() error {
		var entries []domain.CastEntry
		if err := s.io.ReadJSONUnlocked(castLedgerPath, &entries); err != nil && !os.IsNotExist(err) {
			return err
		}

		introMap := make(map[string]string, len(intros))
		for _, in := range intros {
			if in.Name != "" {
				introMap[in.Name] = in.BriefRole
			}
		}

		index := make(map[string]int, len(entries))
		for i, e := range entries {
			index[e.Name] = i
			for _, alias := range e.Aliases {
				index[alias] = i
			}
		}

		seen := make(map[string]bool, len(characters))
		for _, name := range characters {
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			if knownCore[name] {
				continue
			}
			if i, ok := index[name]; ok {
				entry := &entries[i]
				if !slices.Contains(entry.AppearanceChapters, chapter) {
					entry.AppearanceChapters = append(entry.AppearanceChapters, chapter)
					entry.AppearanceCount = len(entry.AppearanceChapters)
					if chapter > entry.LastSeenChapter {
						entry.LastSeenChapter = chapter
					}
					if chapter < entry.FirstSeenChapter || entry.FirstSeenChapter == 0 {
						entry.FirstSeenChapter = chapter
					}
				}
				if entry.BriefRole == "" {
					if br, ok := introMap[name]; ok && br != "" {
						entry.BriefRole = br
					}
				}
				continue
			}
			entries = append(entries, domain.CastEntry{
				Name:               name,
				BriefRole:          introMap[name],
				FirstSeenChapter:   chapter,
				LastSeenChapter:    chapter,
				AppearanceCount:    1,
				AppearanceChapters: []int{chapter},
			})
		}
		return s.io.WriteJSONUnlocked(castLedgerPath, entries)
	})
}

// RecentActive returns the top N recently active supporting characters (descending by LastSeenChapter).
// Used by novel_context to recall "recently appeared supporting characters" that Writer might need when writing the next chapter.
//
// Entries already promoted to characters.json (Promoted=true) are skipped to avoid duplicate recall.
func (s *CastStore) RecentActive(limit int) ([]domain.CastEntry, error) {
	if limit <= 0 {
		return nil, nil
	}
	entries, err := s.Load()
	if err != nil {
		return nil, err
	}
	active := entries[:0:0]
	for _, e := range entries {
		if e.Promoted {
			continue
		}
		active = append(active, e)
	}
	if len(active) == 0 {
		return nil, nil
	}
	sort.Slice(active, func(i, j int) bool {
		if active[i].LastSeenChapter != active[j].LastSeenChapter {
			return active[i].LastSeenChapter > active[j].LastSeenChapter
		}
		return active[i].AppearanceCount > active[j].AppearanceCount
	})
	if len(active) > limit {
		active = active[:limit]
	}
	return active, nil
}
