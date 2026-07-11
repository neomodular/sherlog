package store

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// commitResolveTimeout bounds the git rev-parse at session creation so a slow or
// hung git invocation can never delay a debug_start (harden-detective-gates D-H).
const commitResolveTimeout = 2 * time.Second

// gitCommit resolves the HEAD commit SHA of the git work tree at cwd using a
// fixed-argv git invocation — no shell — with a short timeout (D-H). Any failure
// (git absent, cwd not a repository, timeout, non-SHA output) yields "" so a
// missing commit is silently omitted and never blocks a session. This is the
// default commit resolver; tests inject a deterministic one via WithCommitResolver
// so the suite never depends on the test environment being a git repository.
func gitCommit(cwd string) string {
	if strings.TrimSpace(cwd) == "" {
		return ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), commitResolveTimeout)
	defer cancel()

	// Fixed argv (no shell interpolation), output discarded on any error (D-H).
	out, err := exec.CommandContext(ctx, "git", "-C", cwd, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	sha := strings.TrimSpace(string(out))
	if !looksLikeSHA(sha) {
		return ""
	}
	return sha
}

// looksLikeSHA reports whether s is a plausible lowercase hex git object name.
// It guards against a git that prints a warning or a symbolic ref where a raw SHA
// was expected — a non-SHA line is treated the same as no commit at all.
func looksLikeSHA(s string) bool {
	if len(s) < 7 || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}
