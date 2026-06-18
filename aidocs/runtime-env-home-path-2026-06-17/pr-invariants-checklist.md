# PR Invariants Checklist: Runtime HOME/PATH Preservation

## Scope

- PR slice: runtime-env-home-path-2026-06-17
- Linked impact report: `aidocs/runtime-env-home-path-2026-06-17/impact-surface-report.md`

## Core Invariants

- Startup sequence remains deterministic and traceable: yes
- Control flow remains explicit: yes
- Shared authorization/business policy logic remains in engine flows: yes
- Permission decisions use protocol-agnostic username: yes
- Per-connector message ordering guarantees preserved: yes
- Config precedence rules remain explicit: yes

## Multi-Protocol / Connector

- Transport-specific internal IDs map explicitly to shared username: n/a
- Cross-protocol identity mapping is explicit (no heuristic inference): n/a
- Connector-local behavior does not bypass engine policy rules: yes
- Cross-connector isolation maintained (if multiple connectors enabled): yes
- Failure in one connector does not terminate others (if multiple enabled): yes

## Startup / Config / Compatibility

- Startup and load order verified against `aidocs/STARTUP_FLOW.md`: yes
- Config default/override behavior validated: n/a
- Operator-visible changes documented: yes
- Compatibility note completed (or explicitly not required): yes

## Tests

- Focused tests added/updated: yes
- Existing relevant tests passing: yes (`go test ./bot`, `go test ./modules/gsh`)
- Broader test pass status recorded: yes (`make`; MCP integration suites `TestShFull`, `TestLuaFull`, `TestJSFull`, `TestGoFull`)

## Documentation

- `aidocs/COMPONENT_MAP.md` updated if component boundaries moved: n/a
- Connector docs updated where behavior changed: n/a
- Other affected docs updated: yes

## Sign-Off

- Residual risks: custom scripts that used `$HOME` for robot-owned files must migrate to `$GOPHER_HOME`.
- Follow-up items: none identified.
