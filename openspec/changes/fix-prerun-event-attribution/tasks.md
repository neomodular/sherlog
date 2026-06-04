# Tasks: fix-prerun-event-attribution

## 1. Store

- [ ] 1.1 Adoption-on-open in OpenRun/OpenOrAttachRun (new-run path only): boundary + 15-min cap, in-memory relabel, orphan flood-buffer re-key with timestamp split and minimum-total disclosure across truncation
- [ ] 1.2 Persistence: adoption marker line in logs.jsonl + replay application; `Adopted` field on ProbeSummary
- [ ] 1.3 Unit tests: fast-repro adoption, boundary protection, cap exclusion, re-attach no-op, restart replay, truncated-buffer split, fully-adopted summary disclosure

## 2. Daemon + MCP

- [ ] 2.1 Carry adopted counts through summary/await endpoints and the await_run/query_logs MCP results
- [ ] 2.2 Integration test: ingest-before-await → await_run returns adopted evidence within debounce

## 3. Skill

- [ ] 3.1 SKILL.md: adopted-evidence interpretation rules (accept-with-label, fixed-check sanity check, re-prompt on inconsistency)
