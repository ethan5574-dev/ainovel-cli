package domain

// Novel represents novel metadata.
type Novel struct {
	Name          string `json:"name"`
	TotalChapters int    `json:"total_chapters"`
}

// OutlineEntry represents an outline entry for a single chapter.
type OutlineEntry struct {
	Chapter   int      `json:"chapter"`
	Title     string   `json:"title"`
	CoreEvent string   `json:"core_event"`
	Hook      string   `json:"hook"`
	Scenes    []string `json:"scenes"`
}

// Character represents character profile.
type Character struct {
	Name        string   `json:"name"`
	Aliases     []string `json:"aliases,omitempty"` // Aliases/titles/nicknames (e.g. "waste boy", "Brother Yan")
	Role        string   `json:"role"`
	Description string   `json:"description"`
	Arc         string   `json:"arc"`
	Traits      []string `json:"traits"`
	Tier        string   `json:"tier,omitempty"` // core / important / secondary / decorative (default: important)
}

// VolumeOutline represents volume outline (long-form layered mode).
type VolumeOutline struct {
	Index int          `json:"index"`
	Title string       `json:"title"`
	Theme string       `json:"theme"` // Core conflict/theme of this volume
	Arcs  []ArcOutline `json:"arcs"`
}

// IsExpanded checks if the volume has been expanded into arcs.
func (v *VolumeOutline) IsExpanded() bool { return len(v.Arcs) > 0 }

// StoryCompass represents the ending direction compass, which replaces the fixed skeleton volume list.
// Architect can update it at volume boundaries, allowing story direction to evolve.
type StoryCompass struct {
	EndingDirection string   `json:"ending_direction"`          // Ending direction (thematic description)
	OpenThreads     []string `json:"open_threads,omitempty"`    // Active long-term plot threads (must resolve before ending)
	EstimatedScale  string   `json:"estimated_scale,omitempty"` // Rough estimated scale (e.g. "estimated 4-6 volumes")
	LastUpdated     int      `json:"last_updated,omitempty"`    // Number of completed chapters when updated
}

// ArcOutline represents arc outline.
type ArcOutline struct {
	Index             int            `json:"index"` // Inside-volume arc sequence number
	Title             string         `json:"title"`
	Goal              string         `json:"goal"`                         // Arc goal (introduction, development, turn, conclusion)
	EstimatedChapters int            `json:"estimated_chapters,omitempty"` // Estimated chapters of the skeleton arc (cleared after expanded)
	Chapters          []OutlineEntry `json:"chapters"`
}

// IsExpanded checks if the arc has been expanded (contains detailed chapters).
func (a *ArcOutline) IsExpanded() bool { return len(a.Chapters) > 0 }

// TotalChapters calculates current planned total chapters of the layered outline.
// Expanded arcs count real chapters, skeleton arcs count EstimatedChapters.
// Progress.TotalChapters uses this to adjust long-form context strategy; playable chapters still come from FlattenOutline.
func TotalChapters(volumes []VolumeOutline) int {
	n := 0
	for _, v := range volumes {
		for _, a := range v.Arcs {
			if a.IsExpanded() {
				n += len(a.Chapters)
			} else {
				n += a.EstimatedChapters
			}
		}
	}
	return n
}

// FlattenOutline flattens the layered outline into a flat chapter list, keeping global chapter numbers sequential.
func FlattenOutline(volumes []VolumeOutline) []OutlineEntry {
	var result []OutlineEntry
	ch := 1
	for _, v := range volumes {
		for _, a := range v.Arcs {
			for _, e := range a.Chapters {
				e.Chapter = ch
				result = append(result, e)
				ch++
			}
		}
	}
	return result
}

// WorldRule represents a world setting rule entry.
type WorldRule struct {
	Category string `json:"category"` // magic / technology / geography / society / other
	Rule     string `json:"rule"`     // Rule description
	Boundary string `json:"boundary"` // Boundary that must not be violated
}
