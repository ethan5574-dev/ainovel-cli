package imp

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/voocel/ainovel-cli/internal/utils"
)

// Default chapter title regex. Covers common Chinese (Chapter/Episode/Section N, Volume N, Prologue, Epilogue, etc.)
// and English (Chapter N, Prologue, Epilogue) titles, compatible with Markdown header prefixes (# / ##),
// Qidian-style txt "Main Chapter N" prefixes, and titles wrapped in brackets like 【】〖〗.
//
// Named groups: subtitle groups take priority over keyword groups (retrieved with priority-based fallback):
//   - cn    numbered chapter subtitle (text after 第X章/回/话/卷/节/幕)
//   - vol   independent volume subtitle (text after 卷X)
//   - sp    special section subtitle (text after Prologue/Epilogue/etc. in Chinese)
//   - en    English chapter subtitle (text after Chapter X / Prologue / Epilogue)
//   - spkw  special section keyword itself (used as title when no subtitle is present, e.g. "楔子", "番外")
//   - enkw  English special section keyword itself (used as title when no subtitle is present, e.g. "Prologue")

// ws represents white spaces: ASCII whitespaces + full-width spaces. Go RE2's \s only includes ASCII whitespaces,
// whereas Chinese typesetting often uses U+3000 ("第一章　风起") to separate title parts.
const ws = `\s\x{3000}`

// cnNum represents digit characters allowed for chapter numbering: Arabic / full-width / simplified Chinese / traditional Chinese (壹贰叁...萬).
const cnNum = `零〇○Ｏ０一二三四五六七八九十百千万两壹贰貳叁參肆伍陆陸柒捌玖拾佰仟萬兩\d`

// sub matches subtitle capture: matches up to the end of the line, but does not consume the right bracket (】〗) to leave it for optional closing brackets.
const sub = `[^】〗\n]*`

var defaultChapterRegex = regexp.MustCompile(
	`(?im)^#{0,2}[` + ws + `]*(?:正文[` + ws + `]*)?[【〖]?[` + ws + `]*(?:` +
		`第\s*(?:[` + cnNum + `]+)\s*(?:章|回|话|卷|节|幕)` +
		`(?:[:：．\.` + ws + `]+(?P<cn>` + sub + `))?` +
		`|` +
		`卷\s*(?:[` + cnNum + `]+)` +
		`(?:[:：．\.` + ws + `]+(?P<vol>` + sub + `))?` +
		`|` +
		`(?P<spkw>序章|序幕|楔子|引子|前言|序言|尾声|终章|后记|番外|外传)` +
		`(?:[:：．\.` + ws + `]+(?P<sp>` + sub + `))?` +
		`|` +
		`(?:Chapter\s+(?:\d+|[IVXLCDM]+)|(?P<enkw>Prologue|Epilogue))` +
		`(?:[:：．\.` + ws + `]+(?P<en>` + sub + `))?` +
		`)[` + ws + `]*[】〗]?[` + ws + `]*$`,
)

// SplitFile splits a single text file into a list of chapters.
func SplitFile(path string) ([]Chapter, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read source: %w", err)
	}
	text := utils.DecodeText(data)
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("source file is empty: %s", path)
	}
	return splitText(text, defaultChapterRegex), nil
}

// splitText is the pure function version of SplitFile, making it easy to unit test.
func splitText(text string, pattern *regexp.Regexp) []Chapter {
	lines := strings.Split(text, "\n")
	type marker struct {
		line  int
		title string
	}
	var marks []marker
	for i, ln := range lines {
		if loc := pattern.FindStringSubmatchIndex(ln); loc != nil {
			marks = append(marks, marker{line: i, title: extractTitle(ln, pattern, loc, len(marks)+1)})
		}
	}
	if len(marks) == 0 {
		return nil
	}

	chapters := make([]Chapter, 0, len(marks))
	for i, m := range marks {
		end := len(lines)
		if i+1 < len(marks) {
			end = marks[i+1].line
		}
		body := strings.Join(lines[m.line+1:end], "\n")
		body = stripTrailingNoise(body)
		body = strings.TrimSpace(body)
		if body == "" {
			continue
		}
		chapters = append(chapters, Chapter{Title: m.title, Content: body})
	}
	return chapters
}

// extractTitle extracts chapter title from the matched line; prioritizes named captures, falls back to chapter number placeholder.
func extractTitle(line string, pattern *regexp.Regexp, loc []int, fallbackNum int) string {
	subnames := pattern.SubexpNames()
	priority := []string{"cn", "vol", "sp", "en", "spkw", "enkw"}
	for _, name := range priority {
		idx := pattern.SubexpIndex(name)
		if idx <= 0 {
			continue
		}
		if loc[2*idx] < 0 {
			continue
		}
		if t := strings.TrimSpace(line[loc[2*idx]:loc[2*idx+1]]); t != "" {
			return t
		}
	}
	// Fallback: take the first non-empty capture group (defensive; default regex named groups cover all branches).
	for i := 1; i < len(subnames); i++ {
		if loc[2*i] < 0 {
			continue
		}
		if t := strings.TrimSpace(line[loc[2*i]:loc[2*i+1]]); t != "" {
			return t
		}
	}
	return fmt.Sprintf("Chapter %d", fallbackNum)
}

// stripTrailingNoise strips common trailing noise (e.g. Project Gutenberg license trailer).
var trailerRe = regexp.MustCompile(`(?im)^\s*Project Gutenberg(?:\(TM\)|™)?[\s\S]*$`)

func stripTrailingNoise(content string) string {
	if loc := trailerRe.FindStringIndex(content); loc != nil {
		return strings.TrimRight(content[:loc[0]], " \t\n")
	}
	return content
}
