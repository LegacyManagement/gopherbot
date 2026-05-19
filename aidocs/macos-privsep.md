# Process Privilege Separation Plan

Status: implementation started. Linux/BSD and macOS use the same process-oriented child role contract, but manual setuid validation is still required before privilege separation is considered production-ready on a specific host/OS deployment.

## Problem

The legacy privilege-separation implementation in `bot/privsep.go` was compiled only on Linux and BSD platforms. It used `setreuid` plus `runtime.LockOSThread()` to make UID changes affect one locked OS thread at a time.

That model is intentionally delicate:

- parent code must know which goroutines may run on credential-mutated threads
- permanently changed threads must never run work for the wrong privilege class
- Correctness depends on Go runtime thread lifetime behavior, not on an explicit process boundary.

The current model removes normal thread-scoped credential switching. Parent code runs as the invoking robot user; file-backed children commit once to their selected role before extension code starts.

## Goal

Move privilege separation away from per-thread UID transitions and toward a one-shot process model for file-backed extensions.

The desired end state:

- The parent engine remains the only policy authority.
- Every extension that is not compiled into the engine crosses a child-process boundary before execution.
- The parent selects the child privilege class before the extension crosses that boundary.
- The child permanently commits to either the invoking robot user or the unprivileged account before starting any interpreter or external command.
- Startup fails closed when privilege separation is active and retained supplementary groups do not match explicit administrator policy.
- Normal task execution does not call `setreuid` inside the multithreaded parent process.
- Linux/BSD and macOS may use different low-level credential setup, but expose the same parent/child execution contract.

## Non-Goals

- Do not introduce long-lived broker processes as the base design.
- Do not make connectors responsible for authorization or privilege decisions.
- Do not move policy, parameter resolution, or secret selection into child processes.
- Do not support running compiled-in Go extensions as unprivileged code. Compiled-in extensions remain trusted engine code.
- Do not treat macOS setuid behavior as equivalent to Linux/BSD without manual proof.
- Do not weaken the existing rule that unprivileged extensions cannot discover broad secret-bearing configuration.

## Proposed Model

Use one child process per file-backed extension invocation.

The parent engine keeps responsibility for:

- message routing
- admin checks
- authorizer and elevator execution order
- pipeline privilege classification
- extension parameter assembly
- secret scoping
- cancellation and timeout policy
- operator-visible pipeline state

The child process handles only:

- committing permanently to the parent-selected privilege class
- verifying its real/effective UID and GID before extension execution
- starting the built-in interpreter for Yaegi/Go, JavaScript, Lua, or Gopherbot shell
- or execing the external interpreter/script path for Ruby, Python, Bash, and other executable extensions
- returning stdout, stderr, exit status, and RPC responses to the parent

The child must not decide whether privileged execution is allowed. It receives only the parent-selected execution role and task-specific execution context.

Privilege separation is UID-only. Child startup verifies the selected UID and verifies that the primary GID remains the invoking robot user's GID. It intentionally does not switch to the unprivileged account's primary GID and does not clear supplementary groups.

## Execution Routing

Current parent-owned task policy remains authoritative:

- `startPipeline` sets pipeline privilege from the starter plugin/job.
- `bot/robot_pipecmd.go` blocks privileged tasks from being added to unprivileged pipelines.
- `runPipeline` and `executeTask` classify task type and execution path.

Under the process-oriented model:

- compiled-in Go plugins/jobs/tasks remain in-process trusted code
- external executable tasks run in a one-shot child for the selected privilege class
- Lua, JavaScript, Gopherbot shell, and interpreted Go run in a one-shot child before their built-in interpreter starts
- external Ruby, Python, Bash, and similar scripts run in a one-shot child before the external interpreter or script is execed
- external plugin default-config retrieval uses the same child boundary, with conservative unprivileged execution unless the extension is explicitly configured as privileged and the parent has already selected the privileged role

The current `pipeline-child-exec` and `pipeline-child-rpc` paths are the natural implementation targets. A shared child credential preamble should run before either child path starts interpreter/runtime work.

## macOS UID-Only Model

The current Darwin model uses a binary owned by `nobody` with the setuid bit set and the setgid bit clear.

At initial exec:

- `RUID` is the invoking user
- `EUID` is `nobody`
- saved UID is `nobody`
- `RGID` is the invoking primary group
- `EGID` is the invoking primary group

The parent swaps only the effective UID back to the invoking user:

- `setreuid(-1, ruid)`

On Darwin this leaves the saved UID as `nobody`, giving the parent enough state to re-exec the setuid binary for children without ever using root.

Each child re-execs the same setuid binary so the kernel restores effective/saved credentials to `nobody`, then permanently commits:

- invoking-user child:
  - `setreuid(ruid, ruid)`
- nobody child:
  - `seteuid(nobodyUID)`
  - `setreuid(nobodyUID, nobodyUID)`

Expected state:

- parent initial `RUID=<robot uid>`, `EUID=<nobody uid>`
- parent after swap `EUID=<robot uid>`
- invoking-user child `RUID=<robot uid>`, `EUID=<robot uid>`, `RGID=<robot gid>`, `EGID=<robot gid>`
- unprivileged child `RUID=<nobody uid>`, `EUID=<nobody uid>`, `RGID=<robot gid>`, `EGID=<robot gid>`

This validates the UID direction for macOS while preserving group readability for robot extension files.

## Group Boundary

Unprivileged children retain the invoking user's primary and supplementary groups. This is intentional in the UID-only model so extensions can read and execute files created with the robot user's group, especially when startup uses `umask 027`.

This also means group membership is not a privilege-separation boundary. Robot privileges that must not be available to unprivileged children must be granted directly to the invoking robot UID, not through groups. `.env` is forced to mode `0400` before loading because it contains `GOPHER_ENCRYPTION_KEY` and must not be readable through retained group access.

## Configuration

There is no active privsep group allow-list configuration. The earlier `PrivsepAllowAllSupplementaryGroups` and `PrivsepAllowedSupplementaryGroups` keys have been removed and now fail as unrecognized config keys.

## Operator Privilege Guidance

Grant privileged robot capabilities to the invoking robot user, not to a group that an unprivileged child might retain.

Recommended patterns:

- grant narrowly scoped sudoers entries directly to the robot username
- grant file ownership or ACL access directly to the robot user where possible
- keep secret files unreadable by broad operator groups
- keep the robot invoking user out of broad administrative groups such as `wheel` unless the deployment intentionally accepts that those group privileges may be visible to unprivileged children on platforms that cannot drop supplementary groups

Avoid patterns:

- granting robot privilege through `%wheel`, `%admin`, or similar broad sudoers groups
- making cloud credential files or deployment keys group-readable by a group retained by unprivileged children
- relying on primary UID/GID changes alone to protect resources that are also readable through supplementary groups

The practical rule is: if an unprivileged child may retain supplementary group membership, group grants are not a reliable separation boundary. Use direct user grants for privileged robot work.

## Linux EC2 Metadata Firewall Note

On Linux hosts running in AWS EC2, instance metadata endpoints can expose temporary credentials for the instance role. AWS documents IMDS endpoints at IPv4 `169.254.169.254` and, when IPv6 IMDS is explicitly enabled on Nitro instances, `[fd00:ec2::254]` (see AWS EC2 [Configure the Instance Metadata Service options](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/configuring-instance-metadata-options.html)).

Deployments that rely on privilege separation should consider UID-scoped firewall rules so unprivileged extension children cannot reach IMDS, while the privileged robot user can if the robot legitimately needs instance role credentials.

Implementation guidance for operators:

- prefer IMDSv2 and least-privilege instance profiles
- block metadata endpoint access for the unprivileged UID with Linux owner-match firewall rules (`iptables`/`nftables`, depending on host policy)
- cover both IPv4 and IPv6 IMDS endpoints when IPv6 metadata is enabled
- treat metadata access as credential access, not ordinary network access

This is an operational hardening recommendation, not a substitute for the engine's secret-scoping rules.

## Platform Mechanics

### macOS

Expected operating model:

1. Install the binary owned by `nobody` with the setuid bit set and setgid bit clear.
2. At startup, detect the inverted setuid-nobody state.
3. Move the parent engine back to the invoking user while preserving the ability to re-exec children through the setuid binary.
4. Run a startup self-check for unprivileged child UID and inherited robot GID.
5. For each file-backed extension invocation, start a child by re-execing the same binary with an internal child command.
6. In the child command, permanently commit to the requested role before starting any interpreter, RPC loop, or external executable.
7. Verify real/effective UID and inherited GID, and fail closed if they do not match the UID-only role contract.

macOS-specific validation still required:

- child process group kill behavior
- code signing, quarantine, and filesystem ownership interactions for setuid binaries
- behavior when `nobody` has UID `-2` as exposed through Go/syscall APIs

### Linux/BSD

Linux/BSD now use the same one-shot child role contract for file-backed extension execution. Limited thread-scoped helpers remain only for parent-owned operations and migration compatibility.

The target process contract should still be the same:

- parent chooses role
- child commits permanently before extension execution
- no file-backed extension runs directly in the multithreaded parent
- thread-scoped UID switching is removed from normal extension execution once process children cover all external/interpreter paths

The low-level Linux/BSD credential sequence may differ from macOS and should be validated independently.

## Security Invariants

- One child invocation has exactly one privilege class.
- No child process may alternate between privileged and unprivileged work.
- A child must verify and report, or at minimum fail closed on, real/effective UID mismatch and unexpected primary GID changes before executing extension code.
- Privsep is UID-only. Primary and supplementary groups are intentionally retained, so they must not be used as the robot privilege boundary.
- `.env` must remain owner-readable only because it contains the robot encryption key.
- Parent must pass only task-specific environment, argv, working directory, and already-selected parameters.
- Parent must not pass raw robot config, provider registries, broad parameter sets, or privilege tokens to children.
- Parent-owned RPC remains the only path for Robot API calls from interpreter-backed children.
- Child processes should run in separate process groups so timeout and admin kill behavior remains scoped.
- Child failure must fail only that invocation and must not cascade across connectors or unrelated pipelines.

## Migration Plan

1. Add a small shared child credential preamble that can commit a just-execed child to either invoking-user or unprivileged role. (Implemented)
2. Remove thread-scoped raise/drop helpers and the `RaisePriv` extension API. (Implemented)
3. Route external executable execution through the credential preamble before `pipeline-child-exec` runs the target command. (Implemented)
4. Route interpreter-backed RPC execution through the credential preamble before `pipeline-child-rpc` starts Yaegi/Go, JavaScript, Lua, or Gopherbot shell runtime work. (Implemented)
5. Route external plugin default-config retrieval through the same child boundary. (Implemented)
6. Remove normal task execution calls to legacy raise/drop helpers after all file-backed execution paths use committed child processes. (Implemented)
7. Retain only minimal startup/child-creation privilege setup code.
8. Remove the earlier `robot.yaml` supplementary-group policy keys now that privsep is UID-only. (Implemented)
9. Enable macOS only after manual validation covers UID, inherited primary GID, process-group cleanup, and setuid binary lifecycle.

## Validation Plan

Automated tests:

- unit tests for role parsing and UID/GID validation logic
- unit tests for UID-only self-check validation
- task-routing tests confirming privileged and unprivileged pipelines select the expected child role
- timeout/kill tests confirming process group cleanup
- regression tests for parameter and secret scoping

Manual setuid tests:

- build the binary
- set owner/setuid according to the target platform procedure and ensure setgid is not set
- run as a non-root robot user
- verify startup logs show expected parent and child UIDs/GIDs
- verify unprivileged children keep the robot GID and run with the unprivileged UID
- run privileged and unprivileged probe extensions
- verify unprivileged probes cannot access privileged-only files or secrets
- verify unprivileged probes cannot read `.env`
- on Linux EC2 deployments, verify UID-scoped firewall rules block IMDS access from the unprivileged UID
- restore binary ownership and setuid bits after testing

## Documentation Updates Required With Implementation

- `aidocs/EXECUTION_SECURITY_MODEL.md`
- `aidocs/PIPELINE_LIFECYCLE.md`
- `aidocs/STARTUP_FLOW.md`
- `aidocs/TESTING_CURRENT.md`
- root `GOALS_v3.md`
