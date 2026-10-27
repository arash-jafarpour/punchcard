package report

import "strings"

func collapseWhitespace(s string) string { return strings.Join(strings.Fields(s), " ") }

// clip truncates s to limit runes, appending an ellipsis when it cuts.
func clip(s string, limit int) string {
	if limit <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "…"
}
