package exp

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/voocel/ainovel-cli/internal/domain"
)

// chapterTitleIndex maps chapter number to title, returns empty if missing.
type chapterTitleIndex map[int]string

func buildTitleIndex(outline []domain.OutlineEntry) chapterTitleIndex {
	idx := make(chapterTitleIndex, len(outline))
	for _, e := range outline {
		if e.Title != "" {
			idx[e.Chapter] = e.Title
		}
	}
	return idx
}

// chapterLocation is the location of a chapter in the layered outline. It only retains volume info required for export layout --
// arcs are not exported (arcs are too detailed for the reader's perspective).
type chapterLocation struct {
	VolumeIdx       int
	VolumeTitle     string
	IsFirstOfVolume bool
}

// buildLocations constructs {chapter -> location} in the order of the layered outline's global chapters.
// Chapter numbers are reconstructed using the same rules as FlattenOutline (cumulative volume/arc order),
// to maintain consistency with chapter numbers in Progress.CompletedChapters. Arcs are still traversed (necessary for calculating global chapter numbers),
// but not included in location -- exports only insert dividers at the start of a volume.
func buildLocations(volumes []domain.VolumeOutline) map[int]chapterLocation {
	if len(volumes) == 0 {
		return nil
	}
	locs := make(map[int]chapterLocation)
	ch := 0
	for _, v := range volumes {
		firstOfVol := true
		for _, a := range v.Arcs {
			for range a.Chapters {
				ch++
				locs[ch] = chapterLocation{
					VolumeIdx:       v.Index,
					VolumeTitle:     v.Title,
					IsFirstOfVolume: firstOfVol,
				}
				firstOfVol = false
			}
		}
	}
	return locs
}

// chapterHeaderRe matches the first line of a Markdown heading with a chapter number (e.g., # Chapter N / # 第N章 / ## Chapter 12 ...).
var chapterHeaderRe = regexp.MustCompile(`^(?i)#+\s+(?:Chapter|第)\s*.+?(?:章|$)`)

// atxTitleRe extracts the text part of an ATX heading (# Title).
var atxTitleRe = regexp.MustCompile(`^#{1,6}\s+(.+?)\s*$`)

// stripChapterTitleHeader strips the first line if it is a chapter title that would duplicate the exporter's unified title.
// Two cases: (1) "# Chapter N ..." / "# 第N章 ..." (with chapter number); (2) a Markdown heading whose text is exactly the chapter title
// (writers often write the raw chapter name as a heading in the first line of the body, e.g. "# Edge Village Life", which duplicates the exporter's
// "Chapter N: Edge Village Life"). Other H1 headings (e.g. "# Prologue") are treated as part of the body and retained.
// The caller is responsible for calling TrimSpace first, so leading blank lines are not considered.
func stripChapterTitleHeader(content, title string) string {
	first, rest, hasNewline := strings.Cut(content, "\n")
	if !isChapterTitleLine(first, title) {
		return content
	}
	if !hasNewline {
		return ""
	}
	return strings.TrimLeft(rest, "\n")
}

func isChapterTitleLine(line, title string) bool {
	if chapterHeaderRe.MatchString(line) {
		return true
	}
	if title = strings.TrimSpace(title); title == "" {
		return false
	}
	m := atxTitleRe.FindStringSubmatch(line)
	return len(m) == 2 && strings.TrimSpace(m[1]) == title
}

// renderTXT joins the final text.
//
// Chapter order is determined by chapters (already sorted and deduplicated by chapter number). bodies/titleIdx/locations
// are handled with a fallback-on-missing strategy: if the title is missing, it only outputs "Chapter N"; if the layered location is missing, it is treated as a flat outline.
func renderTXT(
	novelName string,
	chapters []int,
	titleIdx chapterTitleIndex,
	locations map[int]chapterLocation,
	bodies map[int]string,
) string {
	var b strings.Builder

	if name := strings.TrimSpace(novelName); name != "" {
		b.WriteString("《")
		b.WriteString(name)
		b.WriteString("》\n\n")
	}

	useLayered := len(locations) > 0

	for i, ch := range chapters {
		if useLayered {
			if loc, ok := locations[ch]; ok && loc.IsFirstOfVolume {
				b.WriteString("\n═══════════════════════════════════════════\n")
				fmt.Fprintf(&b, "           Volume %d  %s\n", loc.VolumeIdx, strings.TrimSpace(loc.VolumeTitle))
				b.WriteString("═══════════════════════════════════════════\n\n")
			}
		}

		title := strings.TrimSpace(titleIdx[ch])
		if title != "" {
			fmt.Fprintf(&b, "Chapter %d  %s\n\n", ch, title)
		} else {
			fmt.Fprintf(&b, "Chapter %d\n\n", ch)
		}

		body := stripChapterTitleHeader(strings.TrimSpace(bodies[ch]), title)
		b.WriteString(body)
		b.WriteString("\n")
		if i < len(chapters)-1 {
			b.WriteString("\n\n")
		}
	}
	return b.String()
}
