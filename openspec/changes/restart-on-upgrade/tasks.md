# Tasks: restart-on-upgrade

## 1. Daemon — watcher + drain

- [x] 1.1 Binary-identity snapshot: capture `os.Executable()` device/inode + mtime + size before the listener opens; portable fallback to mtime+size where the syscall view is unavailable; startup resolution failure disables the watcher with one log line (never fatal); identity comparison helper with table-driven tests (replaced, deleted, untouched, touch-only)
- [x] 1.2 Watcher goroutine in `daemon.Run` (retention-ticker precedent): fixed 30s constant, injectable path + interval + stop signal for tests — tests never watch the real test binary and never assume port 2218
- [x] 1.3 Await in-flight gauge on the await engine (`atomic.Int64`, `Server.subscribers` precedent): incremented for the life of each blocking await; test that concurrent awaits count correctly
- [x] 1.4 Drain-and-exit: on trigger, mark draining, log one stderr line naming old vs observed identity, exit via a clean `Run` return (code 0) on the first tick with gauge zero; awaits during drain still served; bounded fallback exits after `await_max_timeout_seconds`; tests — in-flight await completes first, fallback fires, new await during drain served

## 2. MCP — stale-client disclosure

- [x] 2.1 `ensureDaemon`: after a successful health check, compare `info.Version` to the compiled version; on mismatch emit one informational stderr line per process (sync.Once), stating a session restart loads the new schemas; no behavioral change; tests for once-only emission and matching-versions silence

## 3. Swap integration

- [x] 3.1 Integration test over an ephemeral listener + temp store root: open a session with an open run, trigger the watcher synthetically, verify clean exit after drain, start a fresh Server over the same root, verify the session and open run replayed and an await re-attaches (the upgrade-mid-investigation scenario end to end)

## 4. Docs (same PR, docs-match-binary convention)

- [x] 4.1 `docs/architecture.md`: daemon-lifecycle section — watcher, drain semantics, the disk-not-versions decision (D-A) and why stale observers never get a vote
- [x] 4.2 `docs/troubleshooting.md`: upgrade flow rewritten — brew upgrade / go install just works within one watch interval; delete every "kill the resident daemon" instruction repo-wide (`rg -l "kill.*daemon"` sweep: README, docs, examples); note the one-time manual kill for daemons predating this change
- [x] 4.3 `CLAUDE.md` dev-loop note updated: `go install` alone now suffices; the old kill-the-daemon dance documented as pre-watcher history

## 5. Validation

- [x] 5.1 Full local suite green: `go build ./... && go vet ./... && go test ./... && gofmt -l .` (empty), plus `go test -race ./...`
- [x] 5.2 Live dogfood: `go install` while the resident daemon runs a real case with an open run → daemon exits on its own within ~30s (watch the log line), next tool call respawns the new binary, `debug_resume` shows the case intact and `await_run` re-attaches; repeat once with an await blocking to observe the drain — done live (wf_37401eee-817): idle swap self-exit 3s after the tick, triggered by the INODE change on a same-size binary (validating device+inode detection); drain timeline install S+53 → drain S+60 → await returned normally S+78 → exit S+91; replayed state intact; judge verdict pass
