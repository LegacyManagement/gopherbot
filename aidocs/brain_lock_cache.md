# Brain Lock and Cache Startup Safety Plan

Status: implementation plan accepted; corresponding engine changes are being
implemented in the brain cache, lock, startup, and shutdown paths.

This document refines the brain cache design around ownership, startup
recovery, clean shutdown, crash restart, and stale local cache detection.

## Plain English Summary

The remote brain lock should identify a specific local cache lineage, not just a
robot name. Each local cache gets a random local nonce/ID, persisted in
`control.json`. When the robot acquires the remote brain lock it writes a fresh
`LockID` tied to that local cache. If the robot crashes, the next process on the
same VM can prove "that held lock was mine" by matching local control state to
the remote lock.

Clean shutdown should no longer delete the lock. It should write the lock as
`released` with the latest database version. A new VM with no local cache can
start cleanly when it sees a released lock: it hydrates the v3 remote brain into
its local cache, acquires the lock, and keeps the startup gate closed until the
cache/lock checks and first plugin initialization are complete.

If a robot starts with an existing local cache and the released remote lock says
the remote database version is newer than the local cache knows about, startup
must hard-fail. That means another robot/cache advanced the brain, and the owner
should run an explicit CLI recovery command such as `gopherbot pull-brain`.

## Goal

Cloud-backed robots should normally read and write through the local persistent
cache, but startup must prove that the local cache is still a valid owner of the
configured remote brain before the robot accepts commands.

The daemon should automatically recover cases that are safe and locally
authoritative, and hard-fail cases that indicate another robot advanced the
remote brain or the local cache is stale.

## Current Problem

The current cached brain already has a durable local outbox: local mutations
write payload, metadata, and an outbox entry before the background sync worker
attempts cloud writes. That outbox is enough to recover known-unsynced writes.

The missing pieces are:

- clean shutdown currently deletes `bot:instance-lock`, losing the version the
  previous owner cleanly released
- a hard crash leaves the lock in a held state, which prevents automatic restart
  even when the same VM and same local cache are recovering their own lock
- startup verifies one checkpoint key, but it does not distinguish:
  - known-unsynced local writes in the outbox
  - the last cloud write we believe succeeded
  - remote brain state advanced by another robot/cache
- `RequireRemoteCleanOnStartup` is too blunt: the desired behavior is not a
  configurable "start dirty" mode, but deterministic repair-or-fail behavior

## Impact Surface Report

Subsystems:

- `bot/brain_cache.go`: local cache control metadata, outbox replay, remote
  checkpoint verification, provider sync policy consumption
- `bot/brain_lock.go`: acquire/release semantics for `bot:instance-lock`
- `bot/bot_process.go`: startup ordering and shutdown ordering around brain
  lock, cache repair, connector readiness, and brain flush
- `bot/handler.go` / startup readiness state: startup gate for commands that
  arrive before brain/cache safety and first plugin initialization are complete
- `bot/brain_provider.go`: construction path for local vs remote cached brains
- `bot/brain_cli.go` and `bot/cli_commands.go`: explicit admin recovery and
  inspection paths
- `robot/brain.go`: remote brain policy additions if provider-specific lock or
  checkpoint retry settings are needed
- `brains/*`: provider-specific sync policy defaults, especially Cloudflare KV

Invariants:

- normal runtime remains v3-only; v2 import/export stays CLI-only
- connectors must not start until the brain lock/cache startup checks pass
- if connectors can receive messages before initial plugin initialization
  completes, the startup gate must reject commands with the operator-facing
  "still starting up" response rather than creating pipelines
- shared authorization, routing, identity, and connector isolation semantics are
  unchanged
- local cache remains the runtime source of truth after startup has passed
- pending outbox entries are repaired from local cache, not treated as a reason
  to serve commands early
- stale local cache or ambiguous ownership causes hard startup failure with an
  actionable CLI recovery message

Concurrency and compatibility:

- only one robot may hold the remote brain lock
- crash recovery may reclaim only the same local cache's own previous held lock
- remote lock state must not be inferred from wall-clock age alone
- provider eventual consistency must be handled by provider policy, not by
  hidden sleeps spread through startup
- existing v3 remote brain data should continue to hydrate a new local cache
- a new VM with no local cache and a released v3 remote lock should hydrate from
  remote, acquire the lock, finish first initialization, and then release the
  startup gate
- existing local caches should be migrated forward by adding new control fields
  with safe zero-value behavior

Docs and tests:

- update `aidocs/STARTUP_FLOW.md`, `devdocs/brain_caching.md`, and
  `UPGRADING-v3.md` when implementing
- add focused unit tests for lock state, crash reclaim, released-version stale
  cache detection, outbox replay, and checkpoint mismatch failure
- run the process-backed memory integration suite after implementation

## Terminology

Database version:

- the monotonically increasing local cache mutation version
- stored in local metadata and remote v3 records
- `NextVersion - 1` is the local cache's latest known mutation version

Outbox:

- durable local queue of mutations that are known not to be confirmed in cloud
- coalesced per key: repeated writes to the same key keep only the latest
  version in the outbox
- replayed from local payload/metadata on startup and by the sync worker

Last successful cloud data write checkpoint:

- the single latest normal memory write/delete that local cache believes was
  durably written to cloud
- excludes `bot:instance-lock`
- used on startup to detect "we thought this cloud write succeeded, but cloud
  does not match"

Brain lock:

- remote record at `bot:instance-lock`
- represents ownership of the cloud brain by one robot/cache lineage
- should be written immediately, outside the delayed ordinary outbox path

Startup gate:

- engine-owned flag that prevents command pipelines while startup is still in a
  protected phase
- responds like shutdown gating, but with exactly
  `(the robot is still starting up, please wait and try your command again later)`
- should remain closed until brain/cache startup safety has passed and the first
  plugin initialization batch has completed

## Proposed Local Control Fields

Extend `brainCacheControl` with fields along these lines:

```go
type brainCacheControl struct {
    // existing fields...
    LastCloudWrite *brainCacheCloudWriteCheckpoint `json:"last_cloud_write,omitempty"`
    CacheNonce     string                          `json:"cache_nonce,omitempty"`
    ActiveLockID   string                          `json:"active_lock_id,omitempty"`
}

type brainCacheCloudWriteCheckpoint struct {
    Key       string    `json:"key"`
    Version   uint64    `json:"version"`
    Checksum  string    `json:"checksum,omitempty"`
    Deleted   bool      `json:"deleted"`
    UpdatedAt time.Time `json:"updated_at"`
    SyncedAt  time.Time `json:"synced_at"`
}
```

Rules:

- generate `CacheNonce` once when creating a local cache and preserve it across
  restarts; this is the local random nonce tying the cache to a VM/cache lineage
- generate a fresh `ActiveLockID` each time startup acquires or reclaims the
  remote lock; include enough local nonce material to prove the lock belongs to
  this cache, but do not expose secrets
- update `LastCloudWrite` only after a successful remote write/delete for a
  non-lock memory and the matching local bookkeeping succeeds
- do not let instance-lock writes overwrite `LastCloudWrite`
- keep `ActiveLockID` after a crash so the next process can identify its own
  previous held lock
- clear or rotate `ActiveLockID` only after writing a clean released lock or
  after successfully replacing a reclaimed held lock

## Proposed Remote Lock Shape

Replace the delete-on-release behavior with an explicit lock state record:

```go
type instanceLockData struct {
    State           string `json:"state"` // held or released
    LockID          string `json:"lock_id"`
    CacheNonceHash  string `json:"cache_nonce_hash,omitempty"`
    RobotName       string `json:"robot_name"`
    FullName        string `json:"full_name,omitempty"`
    Hostname        string `json:"hostname"`
    PID             int    `json:"pid"`
    StartMode       string `json:"start_mode"`
    InstallPath     string `json:"install_path"`
    HomePath        string `json:"home_path"`
    ConfigPath      string `json:"config_path"`
    Version         string `json:"version"`
    Commit          string `json:"commit"`
    StartTime       string `json:"start_time,omitempty"`
    AcquiredAt      string `json:"acquired_at,omitempty"`
    ReleasedAt      string `json:"released_at,omitempty"`
    DatabaseVersion uint64 `json:"database_version"`
}
```

Compatibility:

- absence of `State` should be treated as legacy held lock data
- an unreadable lock remains a hard failure, as today
- v2/unversioned remote memories remain CLI-only migration cases
- a released v3 lock with no local cache is a valid first-start/new-VM path

## Startup Algorithm

Startup for cloud-backed cached brains should be:

1. Keep the startup gate closed.
2. Open the local cache if present. If it does not exist, hydrate a complete
   cache from the v3 remote before command readiness; v2/unversioned remote
   records still hard-fail with the CLI migration message.
3. Read the remote `bot:instance-lock` directly after the cache is available.
4. If this startup hydrated a new local cache:
   - a remote `held` lock is still a hard failure because this VM cannot prove
     ownership of that lock
   - a remote `released` lock is the normal new-VM path and is accepted when
     its database version is covered by the hydrated cache
   - if the remote contains v2/unversioned records, hard fail and instruct the
     owner to run the CLI migration path
5. If the lock is `held` by a different `LockID` or cache nonce, hard fail with the existing
   operator diagnostics.
6. If the lock is `held` by the same `ActiveLockID` and cache nonce, treat this as crash
   recovery:
   - log warnings that the previous process did not release the lock
   - optionally, when on the same host, confirm the recorded PID is gone
   - continue only if the local cache/provider identity matches
7. If the lock is `released` and `DatabaseVersion` is greater than the local
   latest database version, hard fail:
   - this means another robot/cache advanced the brain
   - the admin should run `gopherbot pull-brain` or choose the correct cache
8. Verify `LastCloudWrite` against the remote record:
   - provider policy may retry for eventual consistency
   - if mismatch remains, hard fail
9. Acquire a new held lock:
   - generate a fresh `LockID`
   - include a non-secret hash of `CacheNonce`
   - write `State: held` plus current local database version
   - store that `LockID` in local control
   - sync the lock immediately, not through delayed ordinary outbox behavior
10. Replay all durable outbox entries:
   - warn for each startup replay or summary of replays
   - use local metadata/payload/tombstone as authoritative
   - update `LastCloudWrite` for normal memory writes that are confirmed
11. Proceed to modules, connectors, and first plugin initialization.
12. Release the startup gate only after:
   - brain/cache safety checks have passed
   - the local cache is complete
   - outbox replay is finished
   - the initial plugin initialization batch has completed

Hard-fail cases should use the same operational stance as failure to obtain the
brain lock: log clearly, exit cleanly, and do not accept commands.

This startup gate is part of this implementation plan. It can be expanded later,
but the initial implementation should at least prevent commands from running
between connector startup and first plugin initialization. If the brain/cache
checks happen before connectors are started, users will not see the gate for
that earlier phase, but the readiness state should still reflect it.

## Shutdown Algorithm

Clean shutdown/restart should be:

1. Stop new queue work and wait for running pipelines, as today.
2. Flush ordinary brain outbox until clean.
3. Write `bot:instance-lock` as `State: released` with:
   - current `LockID`
   - current local latest database version
   - release timestamp
   - identifying robot information
4. Sync the released lock immediately.
5. Flush again if needed and shutdown the brain provider.

The released lock is intentional persistent metadata. It should not be deleted.

## Cloud Provider Retry Policy

Add provider-owned policy fields only if needed, for example:

```go
type BrainSyncPolicy struct {
    // existing fields...
    CheckpointVerifyRetries int
    CheckpointVerifyDelay   time.Duration
}
```

Defaults:

- DynamoDB and Firestore can use low retry counts or no special delay when
  reads are strongly consistent enough for this use
- Cloudflare KV should use longer retry behavior because global propagation can
  lag after writes

Retry is only for verifying a record that should already exist. It should not
turn stale-cache or wrong-owner cases into silent auto-pulls.

## Removing `RequireRemoteCleanOnStartup`

`RequireRemoteCleanOnStartup` is removed from engine configuration after this
change.

Reasoning:

- if outbox entries exist, replay them before readiness
- if the local cache is stale relative to a released lock, hard fail
- if the last successful cloud data write cannot be verified, hard fail
- there is no useful production mode where the robot knowingly starts while
  cloud persistence is suspect

The replacement invariant is:

> Startup either proves/reconstructs known local cloud state before readiness,
> or it exits with an actionable recovery message.

## CLI Recovery and Inspection

Normal startup should not auto-pull remote data when it detects another robot
advanced the brain. Recovery remains explicit:

- `gopherbot pull-brain` refreshes local cache from a v3 remote or imports v2
  through the CLI-only path
- `gopherbot restore-brain -remote-format v3` writes local cache to the v3
  remote when the owner intentionally chooses local cache as authoritative
- `gopherbot fetch -validate-cloud <key>`, `fetch -cloud <key>`,
  `fetch -cloud -update-cache <key>`, and `list -cloud` remain inspection and
  targeted repair tools

Potential new CLI helpers:

- `gopherbot brain-status` to report local version, lock state, last cloud
  checkpoint, outbox count, and provider identity
- `gopherbot repair-brain-lock` only if the owner explicitly needs a manual
  stale-lock release path; this should require clear confirmation or flags and
  should not be used by normal startup

## Implementation Slices

1. Add lock/checkpoint metadata types and local control persistence.
2. Add `CacheNonce` generation and persistence for local cache lineage.
3. Make sync update `LastCloudWrite` for successful non-lock cloud writes.
4. Change clean shutdown to write a released lock instead of deleting it.
5. Add startup lock-state evaluation before connector/module startup:
   - held by other lock ID -> fail
   - held by same lock ID -> crash reclaim
   - released ahead of local version -> fail
   - no local cache plus released v3 lock -> hydrate and proceed
6. Add checkpoint verification with provider retry policy.
7. Replay durable outbox before readiness.
8. Integrate startup gate through first plugin initialization.
9. Remove `RequireRemoteCleanOnStartup`.
10. Update CLI/docs/tests.

## Test Plan

Focused unit tests:

- clean shutdown writes released lock with database version
- startup fails when released lock version is greater than local latest version
- startup reclaims held lock with matching local `ActiveLockID`
- startup fails on held lock with different `LockID`
- new VM with no local cache hydrates from released v3 remote and then starts
- new VM with no local cache fails when remote lock is held
- startup verifies last successful cloud data write
- startup hard-fails after retry when checkpoint mismatch persists
- startup replays outbox entries before readiness
- instance lock writes do not overwrite `LastCloudWrite`
- legacy lock record without state is treated as held
- startup gate rejects commands until first plugin initialization completes

Process-backed integration:

- run `TestMemory`
- add a focused suite if unit coverage cannot prove startup/restart behavior
  through real process lifecycle

Required final validation after implementation:

- `go test ./bot ./brains/... ./robot`
- `go test ./...`
- `helpers/check-docs-hygiene.sh`
- `make`
- MCP `run_integration_suite` for `TestMemory`

## Open Questions

- Should same-host crash reclaim verify the recorded PID is gone, or is matching
  `ActiveLockID` sufficient?
- Should manual stale-lock repair be a separate CLI command, or should existing
  `delete bot:instance-lock` remain the documented emergency escape hatch?
- Should lock state be excluded from normal `list` output, or is keeping it as a
  visible `bot:*` memory useful for operators?
