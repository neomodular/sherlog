# case-board-ui (delta)

## ADDED Requirements

### Requirement: Health view
The Case Board SHALL provide a Health view (`#/health`, linked from the main navigation) rendering /api/stats as: a status header (mascot + "on the case" when all self-checks pass; the failing check's detail in plain text otherwise), vitals with live-ticking uptime, effective config with per-key sources, storage panel (data path, disk usage, session counts, total events, field-notes count), activity panel (last event, trailing-hour events, SSE subscribers, open run), and a stale-probes count linking to the Stale Probes view.

#### Scenario: Healthy daemon at a glance
- **WHEN** the user opens #/health on a healthy daemon
- **THEN** the header shows the mascot with "on the case" and every panel renders from /api/stats without external requests

#### Scenario: Failing self-check surfaces
- **WHEN** a self-check reports ok:false
- **THEN** the header replaces "on the case" with the check's detail text

### Requirement: Polite polling
The Health view SHALL poll /api/stats every ~5 seconds only while visible (pausing via the Page Visibility API), tick uptime client-side between polls, and re-render only when data changes.

#### Scenario: Hidden tab stops polling
- **WHEN** the Health view's tab is hidden
- **THEN** polling pauses until the tab is visible again
