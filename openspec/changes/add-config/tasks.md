# Tasks: add-config

## 1. Config package

- [x] 1.1 `internal/config`: typed schema, defaults, strict load (DisallowUnknownFields), env>file>default resolution into an immutable Effective struct, validation rules
- [x] 1.2 Unit tests: absent file, unknown key, precedence, range validation

## 2. CLI

- [x] 2.1 `sherlog config list|get|set` with source display, validation errors, atomic write
- [x] 2.2 CLI tests (table-driven over a temp config path)

## 3. Wiring

- [x] 3.1 Inject flood_keep into store, debounce/max-timeout into await engine, port into daemon startup (defaults pinned by existing tests)
- [x] 3.2 Retention pruning: closed-only, startup + 24h ticker, logged deletions; tests incl. open-session immunity
- [x] 3.3 Effective config (values + sources) on /health
- [x] 3.4 `debug_start` response gains preferences block

## 4. Skill + docs

- [x] 4.1 SKILL.md: minimal-mode presentation rules (theming off, discipline identical, functional lines kept), color handling
- [x] 4.2 README: config section (file, keys, CLI, precedence, retention warning)
