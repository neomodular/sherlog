package store

import (
	"sort"
	"strings"
	"unicode"
)

// recallMaxResults caps how many past cases recall surfaces (design D5: top 3).
const recallMaxResults = 3

// recallMinScore is the floor a match must clear to be returned (design D5).
// Recall is an advisory lead, never evidence, so a weak overlap is dropped rather
// than padded: the threshold is the count of distinct query tokens that must also
// appear in a case (weight-summed), tuned to require a genuine shared term rather
// than an incidental stopword-survivor collision.
const recallMinScore = 1.0

// RecallMatch is one closed case surfaced as possibly related to a new bug
// (case-recall spec). It carries exactly what debug_start needs to cite a prior
// lead — id, the old description, and the recorded resolution — plus the score so
// a caller can rank or display confidence.
type RecallMatch struct {
	SessionID string `json:"session_id"`
	// Title identifies the recalled case so the skill cites it by name rather than
	// by a paragraph of description (add-case-titles D5). Always non-empty: a
	// title-less legacy case carries its description-derived fallback.
	Title       string  `json:"title"`
	Description string  `json:"description"`
	RootCause   string  `json:"root_cause,omitempty"`
	FixSummary  string  `json:"fix_summary,omitempty"`
	Score       float64 `json:"score"`
}

// Recall searches closed, solved sessions for keyword similarity to query and
// returns the top matches above the minimum score (design D5: keyword scoring, no
// embeddings). Only closed sessions with a recorded resolution participate — a
// closed-unsolved case has no root cause to match (session-state spec). The score
// is a weighted term-frequency overlap: each distinct query token contributes its
// frequency in the case's searchable text (description + root cause + confirmed
// hypothesis statement), so a case repeating a shared symptom outranks one
// mentioning it once. Returns an empty slice when nothing clears the threshold —
// never weak matches padded to fill the list (case-recall spec).
func (s *Store) Recall(query string) []RecallMatch {
	queryTokens := tokenSet(query)
	if len(queryTokens) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var matches []RecallMatch
	for id, entry := range s.sessions {
		sess := entry.session
		// Only closed-solved cases participate: an open or unsolved case has no
		// resolution to offer as a lead.
		if sess.ClosedAt == nil || sess.Resolution == nil {
			continue
		}

		score := recallScore(queryTokens, searchableText(sess))
		if score < recallMinScore {
			continue
		}
		matches = append(matches, RecallMatch{
			SessionID:   id,
			Title:       effectiveTitle(sess),
			Description: sess.Description,
			RootCause:   sess.Resolution.RootCause,
			FixSummary:  sess.Resolution.FixSummary,
			Score:       score,
		})
	}

	// Highest score first; break ties by session ID so output is deterministic.
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Score != matches[j].Score {
			return matches[i].Score > matches[j].Score
		}
		return matches[i].SessionID < matches[j].SessionID
	})
	if len(matches) > recallMaxResults {
		matches = matches[:recallMaxResults]
	}
	return matches
}

// searchableText is the corpus recall scores a closed case against (design D5):
// its title, bug description, recorded root cause, and the confirmed hypothesis's
// statement when one is identified. The title joins the corpus because titles are
// short, high-signal tokens (add-case-titles D5); effectiveTitle is used so a
// title-less legacy case still contributes its derived summary. Joined with spaces
// so tokenization sees them as one bag of words.
func searchableText(sess *Session) string {
	parts := []string{effectiveTitle(sess), sess.Description}
	if sess.Resolution != nil {
		parts = append(parts, sess.Resolution.RootCause)
		if id := sess.Resolution.ConfirmedHypothesisID; id != "" {
			for _, h := range sess.Hypotheses {
				if h.ID == id {
					parts = append(parts, h.Statement)
					break
				}
			}
		}
	}
	return strings.Join(parts, " ")
}

// recallScore sums, over each distinct query token, its frequency in the case
// text (weighted TF overlap, design D5). A query term absent from the case
// contributes zero; a term appearing twice contributes two. Distinct query tokens
// avoid double-counting a repeated query word.
func recallScore(queryTokens map[string]struct{}, text string) float64 {
	caseFreq := map[string]int{}
	for _, tok := range tokenize(text) {
		caseFreq[tok]++
	}
	var score float64
	for qt := range queryTokens {
		score += float64(caseFreq[qt])
	}
	return score
}

// tokenSet is the distinct lowercased, stopword-stripped tokens of s.
func tokenSet(s string) map[string]struct{} {
	set := map[string]struct{}{}
	for _, tok := range tokenize(s) {
		set[tok] = struct{}{}
	}
	return set
}

// tokenize lowercases, splits on any non-alphanumeric boundary, drops stopwords,
// and drops single-character tokens (design D5: lowercase, stopword-stripped).
// Single characters carry no symptom signal and only add noise.
func tokenize(s string) []string {
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if len(f) < 2 {
			continue
		}
		if _, stop := stopwords[f]; stop {
			continue
		}
		out = append(out, f)
	}
	return out
}

// stopwords is a small closed set of high-frequency English words that carry no
// discriminating symptom signal (design D5: deliberately dumb, explainable). Kept
// minimal on purpose; expand only if real usage shows noise.
var stopwords = map[string]struct{}{
	"the": {}, "and": {}, "for": {}, "are": {}, "but": {}, "not": {},
	"you": {}, "all": {}, "can": {}, "her": {}, "was": {}, "one": {},
	"our": {}, "out": {}, "has": {}, "had": {}, "his": {}, "him": {},
	"its": {}, "who": {}, "did": {}, "yes": {}, "she": {}, "too": {},
	"use": {}, "way": {}, "why": {}, "this": {}, "that": {}, "with": {},
	"from": {}, "have": {}, "when": {}, "what": {}, "your": {}, "they": {},
	"them": {}, "then": {}, "than": {}, "were": {}, "will": {}, "been": {},
	"into": {}, "some": {}, "such": {}, "only": {}, "does": {}, "after": {},
	"before": {}, "while": {}, "about": {}, "which": {}, "their": {},
}
