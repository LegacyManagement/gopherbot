# Compatibility Note: Runtime HOME/PATH Preservation

## Change Summary

- Change: File-backed extension runtimes preserve parent `HOME` and `PATH` when those variables are set. `GOPHER_HOME` remains the robot home/root directory.
- Why: Overloading `HOME` with the robot directory surprises local development and host tools such as `kubectl`, `git`, and cloud CLIs.
- Effective date/commit: pending merge.

## What Stayed Compatible

- Unchanged behaviors:
  - `GOPHER_HOME`, `GOPHER_CONFIGDIR`, `GOPHER_INSTALLDIR`, and `GOPHER_WORKSPACE` remain robot context values.
  - Working-directory behavior for `Homed` and `SetWorkingDirectory` is unchanged.
  - Privsep child boundaries, role selection, and sensitive `GOPHER_*` scrubbing are unchanged.
- Unchanged config/env surfaces:
  - No config key or Robot API signature changes.

## What Changed

- Behavior differences:
  - `$HOME` no longer defaults to `$GOPHER_HOME` for extension runtimes.
  - `$PATH` is explicitly passed through to runtime/configure environments when present.
- Startup/config/default differences:
  - No startup order or config merge changes.
- Identity/routing/connector differences:
  - None.

## Operator Actions Required

- Required config changes: none.
- Optional config changes:
  - Update custom scripts that use `$HOME` for robot-owned files to use `$GOPHER_HOME`.
  - Pin `HOME` or `PATH` in service units or launch wrappers when production needs a controlled host-tool environment.
- Environment variable changes:
  - `HOME` and `PATH` now behave like inherited parent process variables.

## Rollout / Fallback

- Recommended rollout sequence:
  - Run focused shell/GSH/runtime tests.
  - Smoke-test local plugins that call host CLIs.
  - Review custom scripts for `$HOME` references that should be `$GOPHER_HOME`.
- Rollback/fallback instructions:
  - Temporarily set `HOME=$GOPHER_HOME` in the robot launch environment if a custom script cannot be updated immediately.
- Known temporary limitations:
  - Scripts that rely on service-manager-specific `HOME`/`PATH` values remain dependent on the service launch configuration.

## Validation

- How to verify success:
  - A file-backed extension printing `HOME`, `PATH`, and `GOPHER_HOME` shows inherited parent `HOME`/`PATH` and robot `GOPHER_HOME`.
- How to detect failure quickly:
  - Host CLIs still looking under the robot directory for user config indicate `HOME` was not inherited.

## References

- Impact report: `aidocs/runtime-env-home-path-2026-06-17/impact-surface-report.md`
- PR checklist: `aidocs/runtime-env-home-path-2026-06-17/pr-invariants-checklist.md`
- Related docs: `aidocs/INTERPRETERS.md`, `aidocs/EXECUTION_SECURITY_MODEL.md`, `UPGRADING-v3.md`

