# Proposal: add-case-board-ui

## Why

Sherlog's investigation state (hypothesis boards, probes, runs, evidence) lives in the daemon but is only visible through MCP tool results inside a Claude conversation. Users can't watch evidence stream in while they reproduce a bug, can't browse what the agent is thinking, and closed cases — a growing archive of solved bugs with root causes — are invisible and unused. The daemon already runs an HTTP server on 127.0.0.1:2218, so a browser UI costs no new process, no new install, and no new attack surface.

## What Changes

- New read-only **Case Board** web UI served by the daemon at `http://127.0.0.1:2218/`: case list (open/closed), case detail (suspects with status and evidence, probes with file:line, run history with verdicts), live evidence tail via SSE while a run is open, side-by-side run comparison, and a stale-probes view. Embedded via `go:embed` — single binary stays single; zero external network requests.
- New **case recall**: closed cases record their resolution (root cause, fix summary); `debug_start` searches closed cases for similar symptoms and includes matches in its response so Claude can say "we solved something like this before."
- New **`diff_runs` MCP tool** + daemon endpoint: per-probe comparison between two runs (fired/not-fired, counts, sample values) — the "what's different between a failing and passing run" query, also rendered visually in the Case Board.
- `debug_end` extended to accept `root_cause` and `fix_summary`; the `/debug` skill records them at case close (feeds recall).
- All mutations remain MCP-only; the UI is strictly read-only.

## Capabilities

### New Capabilities

- `case-board-ui`: The read-only browser UI served by the daemon — case list/detail, live evidence tail, run comparison view, stale-probes view.
- `case-recall`: Similarity search over closed cases (symptom + root-cause keywords) surfaced through `debug_start`.

### Modified Capabilities

- `session-state`: ADDED — sessions record a resolution (root cause, fix summary, confirmed hypothesis) at close.
- `log-query`: ADDED — run diff computation (per-probe presence/count/sample deltas between two runs).
- `mcp-server`: ADDED — `diff_runs` tool; `debug_end` accepts resolution fields; `debug_start` response includes similar past cases.
- `debug-skill`: ADDED — the skill supplies root cause + fix summary at `debug_end` and may cite recalled cases when forming hypotheses.

## Impact

- Code: `internal/daemon` (UI serving, SSE, diff endpoint, recall search), `internal/store` (resolution record, diff/search logic), `internal/mcp` (tool changes), new `internal/daemon/ui/` embedded assets, `skills/debug/SKILL.md`.
- No new dependencies (vanilla JS + SSE, `go:embed`); binary size grows by the embedded assets (small).
- Security posture unchanged: loopback-only, read-only UI, session data already local. UI exposes case data to any local browser user — same trust boundary as the files in `~/.sherlog/`.
- MCP tool schema additions are backward-compatible (new optional fields/tools).
