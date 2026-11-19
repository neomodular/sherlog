# case-stats (delta)

## ADDED Requirements

### Requirement: Stats report verdict provenance
Stats SHALL report the share of closed runs by verdict source (`witness`, `agent`, `unknown`) and the number of `solved` cases whose passing fix check was witnessed, and the definitions SHALL state that `solved` does not by itself imply a witnessed verdict.

#### Scenario: Provenance beside the rate
- **WHEN** 6 cases are solved and 4 of their passing fix checks were witnessed
- **THEN** stats report 4 of 6 solved cases as witnessed alongside the verified solve rate
