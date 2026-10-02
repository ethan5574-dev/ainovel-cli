package utils

// ThinkingSep separates thinking text from prose.
// Observer inserts it before thinking segments; TUI uses it to switch rendering styles.
const ThinkingSep = "\x02"

// TruncateRunes truncates by rune and adds an ellipsis; text within n runes remains unchanged.
func TruncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}
