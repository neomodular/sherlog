# Tasks: daemon-self-heal-on-upgrade

## 1. Daemon shutdown endpoint

- [x] 1.1 Server plumbing: give `NewServer` a shutdown-request signal (closed-once `chan struct{}` exposed to `daemon.Run`); handler for `POST /api/shutdown` writes `200 {"ok":true}`, then signals; non-POST gets method-not-allowed (specs/log-ingest: Method discipline)
- [x] 1.2 `daemon.Run`: goroutine selecting on the signal — `srv.Shutdown` with ~2 s budget, then `srv.Close()`; `Run` returns nil so the process exits 0 (specs/log-ingest: bounded drain, long-poll cannot stall)
- [x] 1.3 Daemon tests: shutdown ack + exit, drain cuts a held long-poll within budget, GET refused, board assets/scripts contain no reference to `/api/shutdown` (read-only board invariant)

## 2. MCP client self-heal

- [x] 2.1 Thread the build version into the client: `newDaemonClient(version)`, stored on `daemonClient`; wire from `mcp.Run`
- [x] 2.2 `ensureDaemon` mismatch path: healthy + version differs → `postShutdown`; 404 → legacy actionable error (version pair + per-platform kill command, one-time wording per specs/mcp-server); connection error → treat as already exiting
- [x] 2.3 Port-free wait: poll fresh dials (inverse of `portOccupiedByForeign`) with ~3 s deadline before `spawnDaemon` + existing `waitForHealth`; clear error if the port never frees
- [x] 2.4 MCP tests: mismatch triggers shutdown→respawn handshake (httptest daemon stub), equal versions (incl. `dev`/`dev`) are a no-op, legacy 404 produces the actionable error, concurrent-restart race converges (second shutdown sees refused connection and proceeds)

## 3. Docs (same change, per repo convention)

- [x] 3.1 `docs/troubleshooting.md`: "I upgraded but still see the old UI/behavior" entry — self-heal is automatic from this release; one-time manual kill for ≤ 0.4.0 daemons with the exact commands; note that a cut `await_run` is safely reissued
- [x] 3.2 `docs/architecture.md`: daemon lifecycle section gains the version handshake and `POST /api/shutdown` in the endpoint list (internal surface, read-only board unchanged)

## 4. Verification

- [x] 4.1 `go test ./...` green; `go vet ./...` clean (also fixed a pre-existing leak: the fire-and-forget test detached copies of the test binary via the real spawn; now stubbed through the new spawn seam)
- [x] 4.2 End-to-end on this machine: started a 0.0.1-old daemon on SHERLOG_PORT=22180, ran the 0.9.9-new binary in mcp mode — its startup warm-up ensureDaemon shut the old daemon down and respawned itself: /health flipped 0.0.1-old → 0.9.9-new, old process exited (count 0)
