# Impact Surface Report: Runtime HOME/PATH Preservation

## 1) Change Summary

- Slice name: runtime-env-home-path-2026-06-17
- Goal: Preserve parent `HOME` and `PATH` for file-backed extension runtimes while keeping `GOPHER_HOME` as the robot home/root directory.
- Out of scope: connector behavior, message routing, identity mapping, authorization/elevation policy, startup ordering, and privilege role selection.

## 2) Subsystems Affected

- Files/directories expected to change:
  - `bot/run_pipelines.go`
  - `bot/calltask.go`
  - `bot/pipeline_rpc_test.go`
  - `bot/extension_env_test.go`
  - `conf/robot.yaml`
  - `aidocs/INTERPRETERS.md`
  - `aidocs/EXECUTION_SECURITY_MODEL.md`
  - `aidocs/PIPELINE_LIFECYCLE.md`
  - `aidocs/EXTENSION_API.md`
  - `UPGRADING-v3.md`
- Key functions/types/symbols:
  - `envPassThrough`
  - `worker.getEnvironment`
  - `buildConfigureEnv`
  - `newPipelineChildRPCCommandForRole`

## 3) Current Behavior Anchors

- Startup/order anchors: unchanged; child command dispatch remains after startup/config/brain/module initialization.
- Routing/message-flow anchors: unchanged; pipelines still enter through `startPipeline -> executeTask -> callTaskThread`.
- Identity/authorization anchors: unchanged; admin, authorizer, and elevator checks remain engine-owned and username-authoritative.
- Connector behavior anchors: unchanged; connectors still only submit normalized inbound messages and own transport context.

## 4) Proposed Behavior

- What changes:
  - Runtime and configure-time extension environments inherit parent `HOME` when present.
  - Runtime and configure-time extension environments inherit parent `PATH` when present.
  - `GOPHER_HOME` remains the robot home/root directory.
- What does not change:
  - `GOPHER_*` context values remain explicit robot runtime values.
  - Sensitive launch env scrub remains in place for child process launch.
  - Privsep child role selection and commitment are unchanged.
  - Working-directory behavior for `Homed` and `SetWorkingDirectory` is unchanged.

## 5) Invariant Impact Check

- Startup determinism preserved?: yes
- Explicit control flow preserved?: yes
- Shared auth/policy remains in engine flows?: yes
- Permission checks remain username-based?: yes
- Connector ordering guarantees preserved?: yes
- Config precedence still explicit?: yes
- Multi-connector isolation preserved (if applicable)?: yes

No architectural invariant is redefined. Extension runtime environment semantics are intentionally changed and documented.

## 6) Cross-Cutting Concerns

- Startup sequencing impact: none.
- Config loading/merge/precedence impact: none.
- Execution ordering impact: none.
- Resource lifecycle impact: none; no new goroutines, processes, sockets, or cleanup paths.

## 7) Concurrency Risks

- Shared state touched: global path variables are read as before; process environment is read through existing helpers.
- Locking/channel/event-order assumptions: unchanged.
- Race/deadlock/starvation risks: none identified.
- Mitigations: focused unit tests cover task env, configure env, and RPC child env inheritance.

## 8) Backward Compatibility

- Existing robots/config expected impact: scripts that used `$HOME` as the robot directory must switch to `$GOPHER_HOME`.
- Behavior changes for operators/users: local development and host tools such as `kubectl`, `git`, and cloud CLIs now use the launching user's normal home and command path.
- Migration/fallback plan: set `HOME`/`PATH` explicitly in the launch environment for production pinning; update robot-owned path references to `GOPHER_HOME`.

## 9) Validation Plan

- Focused tests: `go test ./bot`, `go test ./modules/gsh`.
- Broader regression tests: `make`; targeted process-backed suites for shell and interpreter runtimes when feasible.
- Manual verification steps: run a local plugin that prints `HOME`, `PATH`, and `GOPHER_HOME` across Lua/GSH/external shell.

## 10) Documentation Plan

- `aidocs/STARTUP_FLOW.md` updates: not needed; startup sequence unchanged.
- `aidocs/COMPONENT_MAP.md` updates: not needed; no component boundaries moved.
- Connector doc updates: not needed; connector behavior unchanged.
- Other docs:
  - `aidocs/INTERPRETERS.md`
  - `aidocs/EXECUTION_SECURITY_MODEL.md`
  - `aidocs/PIPELINE_LIFECYCLE.md`
  - `aidocs/EXTENSION_API.md`
  - `UPGRADING-v3.md`
  - `conf/robot.yaml` comments

## 11) Waiver

- Waived by: n/a
- Reason: n/a
- Scope limit: n/a

