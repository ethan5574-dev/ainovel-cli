package domain

import (
	"slices"
	"strings"
)

// CastEntry projects supporting-character information from accepted chapter records.
//
// Decoupled from Character (core profiles maintained by Architect in characters.json):
//   - CastEntry deterministically records named minor characters from ChapterRecord
//   - Character stores explicitly planned arcs, traits, and tiers for main/key supporting characters
//
// Character takes precedence for matching names to avoid duplication.
type CastEntry struct {
	Name             string `json:"name"`
	BriefRole        string `json:"brief_role,omitempty"` // Brief role supplied by Writer; may be filled later but is never overwritten
	FirstSeenChapter int    `json:"first_seen_chapter"`
	LastSeenChapter  int    `json:"last_seen_chapter"`
	// AppearanceCount derives from len(AppearanceChapters).
	AppearanceCount    int   `json:"appearance_count"`
	AppearanceChapters []int `json:"appearance_chapters"`
}

// CastIntro is Writer's introduction of a new character at commit_chapter.
// Projection uses the earliest nonempty introduction.
type CastIntro struct {
	Name      string `json:"name"`
	BriefRole string `json:"brief_role"`
}

// ProjectCast rebuilds the cast from accepted records, independently of input order.
func ProjectCast(records []ChapterRecord, characters []Character) []CastEntry {
	records = slices.Clone(records)
	slices.SortFunc(records, func(a, b ChapterRecord) int { return a.Chapter - b.Chapter })

	core := make(map[string]bool)
	for _, character := range characters {
		core[character.Name] = true
		for _, alias := range character.Aliases {
			core[alias] = true
		}
	}

	entries := make(map[string]*CastEntry)
	for _, record := range records {
		intros := make(map[string]string)
		for _, intro := range record.Facts.CastIntros {
			intros[intro.Name] = intro.BriefRole
		}
		seen := make(map[string]bool)
		for _, name := range record.Facts.Characters {
			if name == "" || core[name] || seen[name] {
				continue
			}
			seen[name] = true
			entry := entries[name]
			if entry == nil {
				entry = &CastEntry{Name: name, BriefRole: intros[name], FirstSeenChapter: record.Chapter}
				entries[name] = entry
			} else if entry.BriefRole == "" {
				entry.BriefRole = intros[name]
			}
			entry.LastSeenChapter = record.Chapter
			entry.AppearanceChapters = append(entry.AppearanceChapters, record.Chapter)
			entry.AppearanceCount = len(entry.AppearanceChapters)
		}
	}

	out := make([]CastEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, *entry)
	}
	slices.SortFunc(out, func(a, b CastEntry) int {
		if a.FirstSeenChapter != b.FirstSeenChapter {
			return a.FirstSeenChapter - b.FirstSeenChapter
		}
		return strings.Compare(a.Name, b.Name)
	})
	return out
}

// RecentCast returns up to limit recently active characters without modifying input.
func RecentCast(entries []CastEntry, limit int) []CastEntry {
	if limit <= 0 {
		return nil
	}
	entries = slices.Clone(entries)
	slices.SortFunc(entries, func(a, b CastEntry) int {
		if a.LastSeenChapter != b.LastSeenChapter {
			return b.LastSeenChapter - a.LastSeenChapter
		}
		if a.AppearanceCount != b.AppearanceCount {
			return b.AppearanceCount - a.AppearanceCount
		}
		return strings.Compare(a.Name, b.Name)
	})
	return entries[:min(limit, len(entries))]
}
