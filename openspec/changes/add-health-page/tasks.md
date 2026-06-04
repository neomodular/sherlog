# Tasks: add-health-page

## 1. Counters + endpoint

- [x] 1.1 Store counters: total events, last-event time, 60-bucket hourly ring (memory-only), open/closed session counts, field-notes count accessor
- [x] 1.2 Daemon: SSE subscriber gauge, self-checks (storage_writable, loopback_only), disk usage with 5s cache, GET /api/stats aggregation; /health untouched
- [x] 1.3 Tests: stats shape, activity counters under ingest, self-check failure reporting, /health byte-compatibility, disk-usage cache

## 2. UI

- [x] 2.1 Health view (#/health + nav link): status header w/ mascot, vitals w/ ticking uptime, config-with-sources, storage, activity, stale-probe link — existing theme/helpers, no external requests
- [x] 2.2 Visibility-aware 5s polling, change-only re-render; asset test updated (embedded routes incl. new view)

## 3. Docs touch

- [x] 3.1 docs/architecture.md endpoint list + docs/troubleshooting.md "check #/health first" pointer
