# log-query (delta)

## ADDED Requirements

### Requirement: Run diff
The daemon SHALL compute a per-probe comparison between two runs of the same session: fired-in-each flags, counts, first/last sample bodies from each side, and a divergence flag (fired in exactly one run, or count ratio ≥ 10×). Diff output SHALL carry the same truncation disclosures as queries. Requests naming runs from different sessions or unknown runs SHALL be rejected with a clear error.

#### Scenario: Differential diagnosis
- **WHEN** a diff is requested between run 1 (verdict reproduced) and run 3 (verdict fixed-check) where probe p3 fired only in run 3
- **THEN** p3 is flagged divergent with its per-run counts and samples, and unflagged probes still report both sides

#### Scenario: Invalid run pair
- **WHEN** a diff is requested for runs belonging to different sessions
- **THEN** the daemon rejects the request with an error identifying the mismatch
