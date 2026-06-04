package store

import "testing"

// closeSolved is a test helper: create a session, optionally set a confirmed
// hypothesis, then close it with the given resolution so recall can match it.
func closeSolved(t *testing.T, s *Store, desc, cwd string, res *Resolution) string {
	t.Helper()
	sess, _, err := s.CreateSession(desc, cwd)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if res != nil && res.ConfirmedHypothesisID != "" {
		// Give the confirmed hypothesis a statement so searchableText can include it.
		if _, err := s.SetHypotheses(sess.ID, []string{"the confirmed suspect statement"}); err != nil {
			t.Fatalf("SetHypotheses: %v", err)
		}
		res.ConfirmedHypothesisID = "h1"
	}
	if _, err := s.CloseSessionWithResolution(sess.ID, res); err != nil {
		t.Fatalf("CloseSessionWithResolution: %v", err)
	}
	return sess.ID
}

// TestRecallSimilarCaseFound covers case-recall scenario "A similar solved case
// exists": a query overlapping a closed case's root cause returns that case with
// its root cause and fix summary.
func TestRecallSimilarCaseFound(t *testing.T) {
	s := newTestStore(t)
	id := closeSolved(t, s, "auth breaks sometimes", "/repo/auth", &Resolution{
		RootCause:  "token refresh race after idle timeout",
		FixSummary: "serialize refresh with a mutex",
	})

	matches := s.Recall("login fails intermittently after idle")
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d: %+v", len(matches), matches)
	}
	m := matches[0]
	if m.SessionID != id {
		t.Errorf("wrong session matched: %q want %q", m.SessionID, id)
	}
	if m.RootCause != "token refresh race after idle timeout" {
		t.Errorf("root cause not returned: %+v", m)
	}
	if m.FixSummary != "serialize refresh with a mutex" {
		t.Errorf("fix summary not returned: %+v", m)
	}
	if m.Score < recallMinScore {
		t.Errorf("score below threshold but returned: %v", m.Score)
	}
}

// TestRecallNothingRelevant covers case-recall scenario "Nothing relevant": when
// no closed case clears the threshold, recall returns an empty list — never weak
// matches padded to fill three slots.
func TestRecallNothingRelevant(t *testing.T) {
	s := newTestStore(t)
	closeSolved(t, s, "memory leak in renderer", "/repo/a", &Resolution{
		RootCause: "unbounded cache growth in the paint loop",
	})
	closeSolved(t, s, "slow startup", "/repo/b", &Resolution{
		RootCause: "synchronous disk scan blocks boot",
	})

	matches := s.Recall("payment webhook signature mismatch")
	if len(matches) != 0 {
		t.Errorf("expected no matches for unrelated query, got %+v", matches)
	}
}

// TestRecallExcludesOpenAndUnsolved verifies recall only considers closed-solved
// cases: an open session and a closed-unsolved session never match even when their
// description overlaps the query (session-state: unsolved excluded from recall).
func TestRecallExcludesOpenAndUnsolved(t *testing.T) {
	s := newTestStore(t)

	// Open session whose description overlaps the query — must be excluded.
	if _, _, err := s.CreateSession("token refresh race after idle", "/repo/open"); err != nil {
		t.Fatalf("create open: %v", err)
	}
	// Closed-unsolved session with overlapping description — must be excluded.
	unsolved, _, _ := s.CreateSession("token refresh race after idle", "/repo/unsolved")
	if _, err := s.CloseSession(unsolved.ID); err != nil {
		t.Fatalf("close unsolved: %v", err)
	}

	matches := s.Recall("token refresh race after idle")
	if len(matches) != 0 {
		t.Errorf("open/unsolved cases must not be recalled: %+v", matches)
	}
}

// TestRecallTopThreeRanked verifies the top-3 cap and score ordering: with more
// than three qualifying cases, recall returns the three highest-scoring, ranked
// by score.
func TestRecallTopThreeRanked(t *testing.T) {
	s := newTestStore(t)
	// Each case shares the query term "deadlock" a different number of times, so
	// weighted TF overlap ranks them strong > medium > weak, and a fifth case with
	// a single overlap is dropped after the top three.
	closeSolved(t, s, "deadlock deadlock deadlock everywhere", "/repo/strong", &Resolution{RootCause: "deadlock on lock ordering"})
	closeSolved(t, s, "deadlock deadlock seen twice", "/repo/medium", &Resolution{RootCause: "deadlock in pool"})
	closeSolved(t, s, "deadlock observed once", "/repo/weak", &Resolution{RootCause: "lock held too long"})
	closeSolved(t, s, "deadlock barely", "/repo/weakest", &Resolution{RootCause: "minor"})

	matches := s.Recall("deadlock")
	if len(matches) != recallMaxResults {
		t.Fatalf("expected top %d, got %d: %+v", recallMaxResults, len(matches), matches)
	}
	// Scores must be non-increasing.
	for i := 1; i < len(matches); i++ {
		if matches[i-1].Score < matches[i].Score {
			t.Errorf("matches not ranked by score: %+v", matches)
		}
	}
	if matches[0].Score < matches[len(matches)-1].Score {
		t.Errorf("top match should have the highest score: %+v", matches)
	}
}

// TestRecallMatchesConfirmedHypothesisStatement verifies the confirmed
// hypothesis's statement is part of the searchable corpus (design D5): a query
// overlapping only that statement still matches.
func TestRecallMatchesConfirmedHypothesisStatement(t *testing.T) {
	s := newTestStore(t)
	sess, _, _ := s.CreateSession("vague symptom", "/repo")
	s.SetHypotheses(sess.ID, []string{"connection pool exhaustion under burst load"})
	s.UpdateHypothesis(sess.ID, "h1", HypothesisConfirmed, "confirmed by p2")
	if _, err := s.CloseSessionWithResolution(sess.ID, &Resolution{
		RootCause:             "ran out of connections",
		ConfirmedHypothesisID: "h1",
	}); err != nil {
		t.Fatalf("close: %v", err)
	}

	matches := s.Recall("requests stall under burst")
	if len(matches) != 1 {
		t.Fatalf("expected match via hypothesis statement, got %+v", matches)
	}
}

// TestRecallEmptyQuery verifies a query with no usable tokens returns nothing
// rather than matching everything.
func TestRecallEmptyQuery(t *testing.T) {
	s := newTestStore(t)
	closeSolved(t, s, "some closed bug", "/repo", &Resolution{RootCause: "a cause here"})
	if m := s.Recall("the and for a"); len(m) != 0 {
		t.Errorf("all-stopword query should match nothing, got %+v", m)
	}
	if m := s.Recall(""); len(m) != 0 {
		t.Errorf("empty query should match nothing, got %+v", m)
	}
}

// TestTokenize guards the tokenizer contract: lowercase, split on non-alphanumeric,
// drop stopwords and single characters.
func TestTokenize(t *testing.T) {
	got := tokenize("The Auth-Token refreshed; race! (idle=30s) a x")
	want := map[string]bool{"auth": true, "token": true, "refreshed": true, "race": true, "idle": true, "30s": true}
	if len(got) != len(want) {
		t.Fatalf("token count %d want %d: %v", len(got), len(want), got)
	}
	for _, tok := range got {
		if !want[tok] {
			t.Errorf("unexpected token %q in %v", tok, got)
		}
	}
}
