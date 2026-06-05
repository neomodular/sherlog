package store

import "strings"

// titleMaxLen is the target length for a derived fallback title (design D1/D3:
// "first ~60 characters, word-boundary truncated"). It bounds the *content*
// length; the appended ellipsis is extra.
const titleMaxLen = 60

// titleEllipsis marks a fallback title that was truncated mid-description so a
// derived title reads as a summary, not a sentence cut off without notice (D1).
const titleEllipsis = "…"

// effectiveTitle returns the session's title for any payload, deriving a fallback
// from the description when the stored title is empty (design D1/D3: read-time
// fallback so every payload carries a non-empty title; no migration write). A
// session with a real title returns it verbatim; a legacy or title-less session
// returns deriveTitle(description).
func effectiveTitle(sess *Session) string {
	if strings.TrimSpace(sess.Title) != "" {
		return sess.Title
	}
	return deriveTitle(sess.Description)
}

// deriveTitle distills a one-line title from a description (design D1): the first
// ~60 characters, truncated at a word boundary with an ellipsis when the
// description is longer. A short description is returned whole (no ellipsis); a
// description with no early word boundary is hard-cut at the limit so a single
// long token cannot defeat truncation. Leading/trailing whitespace and interior
// newlines are normalized so a multi-line description yields a single clean line.
func deriveTitle(description string) string {
	// Collapse all runs of whitespace (including the soft-structure newlines) into
	// single spaces so the derived title is one tidy line.
	collapsed := strings.Join(strings.Fields(description), " ")
	if collapsed == "" {
		return ""
	}
	if len(collapsed) <= titleMaxLen {
		return collapsed
	}

	// Truncate to the limit, then back up to the last space so we cut on a word
	// boundary rather than mid-word. If there is no space in the window, hard-cut.
	cut := collapsed[:titleMaxLen]
	if sp := strings.LastIndex(cut, " "); sp > 0 {
		cut = cut[:sp]
	}
	return strings.TrimRight(cut, " ") + titleEllipsis
}
