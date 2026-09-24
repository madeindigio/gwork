package mcpserver

import "unicode/utf8"

// DefaultMaxChars is the default max_chars for long text fields returned by
// tools (message bodies, file contents).
const DefaultMaxChars = 20000

// TruncateText cuts s to at most maxChars characters (runes) and reports
// whether it was truncated. maxChars <= 0 means no limit. The cut never
// splits a UTF-8 sequence.
func TruncateText(s string, maxChars int) (string, bool) {
	if maxChars <= 0 || utf8.RuneCountInString(s) <= maxChars {
		return s, false
	}
	n := 0
	for i := range s {
		if n == maxChars {
			return s[:i], true
		}
		n++
	}
	return s, false
}

// effectiveMaxChars returns n, or DefaultMaxChars when n <= 0. Tools use it
// to interpret an omitted max_chars input.
func effectiveMaxChars(n int) int {
	if n <= 0 {
		return DefaultMaxChars
	}
	return n
}

// truncateEach cuts every text to maxChars characters (0 or less means
// DefaultMaxChars) and reports whether any was cut. It is used for lists
// of independent texts (chat messages, a message's body and HTML) that
// share a single truncated flag.
func truncateEach(maxChars int, texts ...*string) bool {
	limit := effectiveMaxChars(maxChars)
	truncated := false
	for _, t := range texts {
		var cut bool
		*t, cut = TruncateText(*t, limit)
		truncated = truncated || cut
	}
	return truncated
}

// truncateShared spends a single budget of characters on texts in order
// (0 or less means DefaultMaxChars): the text that exceeds the remaining
// budget is cut and the following ones are emptied. It reports whether
// anything was cut. It is used when the total size of related texts (the
// bodies of a thread) must stay bounded.
func truncateShared(budget int, texts ...*string) bool {
	budget = effectiveMaxChars(budget)
	truncated := false
	for _, t := range texts {
		if budget <= 0 {
			if *t != "" {
				*t, truncated = "", true
			}
			continue
		}
		var cut bool
		*t, cut = TruncateText(*t, budget)
		truncated = truncated || cut
		budget -= utf8.RuneCountInString(*t)
	}
	return truncated
}
