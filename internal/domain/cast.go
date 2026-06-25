package domain

// CastEntry is a supporting character record in the cast ledger.
//
// Decoupled from Character (characters.json, maintained by Architect):
//   - CastEntry is automatically accumulated by the commit_chapter tool, recording "named minor characters that appeared"
//   - Character is explicitly designed by the Architect, recording personality arcs/traits/tiers of main and key supporting characters
//
// When names duplicate, Character takes precedence (core characters do not enter the cast_ledger) to avoid duplication.
type CastEntry struct {
	Name string `json:"name"`
	// Aliases is currently not used; reserved for future "user steer merge alias" tool
	// (e.g. declaring 'Manager Li' and 'Old Li' as the same person). MergeAppearances already supports alias lookup.
	Aliases          []string `json:"aliases,omitempty"`
	BriefRole        string   `json:"brief_role,omitempty"` // One-line description (initially filled by Writer on first appearance, can be completed later; won't be overwritten)
	FirstSeenChapter int      `json:"first_seen_chapter"`
	LastSeenChapter  int      `json:"last_seen_chapter"`
	// AppearanceCount is derived from len(AppearanceChapters), synchronized during merge.
	// Keep as an explicit field for easy UI/JSON reading without recalculating every time.
	AppearanceCount    int   `json:"appearance_count"`
	AppearanceChapters []int `json:"appearance_chapters"`
	// Promoted marks that this entry has been promoted to characters.json. RecentActive will skip these entries
	// to avoid duplicate recall with the core ledger. Currently, the promotion pipeline is not implemented; this field is a reserved hook.
	Promoted bool `json:"promoted,omitempty"`
}

// CastIntro is the brief description declared by the Writer when committing a chapter for a newly appearing character.
// Adopted only when the name appears for the first time or when BriefRole in the ledger is still empty.
type CastIntro struct {
	Name      string `json:"name"`
	BriefRole string `json:"brief_role"`
}
