# Brain Cache Implementation Plan

Status: implementation blueprint and current design record. The core cache,
runtime provider contract, CLI migration commands, and shipped provider support
are implemented in the v3 codebase; the slice list remains as a traceable
handoff checklist for future hardening.

## Goal

Gopherbot should make cloud-backed brains feel local during normal operation:

- local reads are fast and do not call the cloud provider
- local writes commit durably before returning to the engine
- cloud writes happen through a durable, metered, coalescing sync queue
- Cloudflare KV can stay within the free tier or an operator-configured budget
- the v3 runtime requires a v3-compatible remote brain for cloud-backed robots
- existing v2 cloud brains import or convert through explicit CLI commands, not
  through compatibility branches in normal startup
- v2 cloud brains can be imported into the local cache before choosing whether
  to write v3 cloud data
- cache-to-cloud CLI operations can write either v2-compatible or v3 cloud
  records so robot owners can roll forward or roll back deliberately
- development robots can keep using a file-only brain with no warning or error

This plan deliberately chooses simple file-key storage for the local cache.
BadgerDB is not required for the expected ChatOps brain size and would add an
unneeded operational dependency. If later evidence shows file IO is insufficient,
the cache store can be hidden behind a narrow interface and replaced without
changing brain semantics.

## Impact Surface Report

### Change Summary

- Slice name: durable local brain cache and cloud sync
- Goal: make local cache the engine-facing brain while cloud providers become
  sync backends
- Out of scope:
  - changing the Robot extension memory API signatures
  - changing connector identity or routing semantics
  - automatic v2 cloud import or cloud format upgrade during normal robot
    startup
  - solving multi-writer cloud conflict resolution beyond the existing
    single-robot brain lock model

### Subsystems Affected

- Brain API and registry:
  - `robot/brain.go`
  - `robot/brains.go`
  - `bot/provider_registrations.go`
- Brain engine flow:
  - `bot/brain.go`
  - `bot/bot_process.go`
  - `bot/brain_lock.go`
  - `bot/cli_commands.go`
- Local cache:
  - new `bot/brain_cache*.go` files, or a small internal package owned by `bot`
  - existing `bot/filebrain.go` should either become local-cache support or be
    replaced by the local cache implementation
- Cloud backends:
  - `brains/cloudflarekv/cloudflarekvbrain.go`
  - `brains/dynamodb/dynamobrain.go`
  - `brains/firestore/firestorebrain.go`
- Config/defaults:
  - `bot/conf.go`
  - `conf/robot.yaml`
  - `conf/brains/*.yaml`
  - `robot.skel/conf/robot.yaml`
  - root `UPGRADING-v3.md`
- Docs:
  - `aidocs/STARTUP_FLOW.md`
  - `aidocs/COMPONENT_MAP.md`
  - `aidocs/TESTING_CURRENT.md` if test assumptions change
  - `aidocs/EXTENSION_API.md` only if extension-visible behavior changes

### Current Behavior Anchors

- Startup order is documented in `aidocs/STARTUP_FLOW.md`.
- `initBot()` in `bot/bot_process.go` loads pre-connect config, initializes the
  configured brain provider, initializes encryption, acquires the brain instance
  lock, then initializes modules and connector runtime support.
- `run()` in `bot/bot_process.go` starts `runBrain()`, restores engine-owned
  memories/subscriptions, starts connectors, loads full config, initializes
  plugins, starts queues, and signals readiness.
- `runBrain()` in `bot/brain.go` serializes engine memory access and provides
  lock-token semantics for `CheckoutDatum` / `UpdateDatum`.
- `getDatum()` and `storeDatum()` in `bot/brain.go` encrypt/decrypt blobs around
  provider calls. Providers store encrypted bytes today.
- `bot:instance-lock` in `bot/brain_lock.go` is currently stored in the brain and
  prevents accidental concurrent robot instances.
- CLI memory commands initialize the provider directly and run the brain loop,
  without connectors or plugins.

### Proposed Behavior

- The engine-facing brain becomes a cached brain wrapper.
- Cloud providers become v3 remote sync backends for normal runtime.
- The local cache stores encrypted payload bytes plus v3 metadata.
- CLI-only conversion paths can read/write v2-compatible remote records.
- `Brain: file` remains valid and means local-only brain, suitable for
  development robots.
- All shipped brain implementations are in scope for this change:
  - `file` becomes the local cache/local-only brain path
  - `mem` remains ephemeral and useful for tests/demo behavior
  - `cloudflare`, `dynamo`, and `firestore` become v3 remote backends with
    metadata/checkpoint support plus CLI-only v2 import/export support
- Cloud-backed steady-state startup with a usable local cache never performs a
  full cloud scan.
- The first startup on a new VM, where no local cache exists, hydrates the local
  cache from the cloud if the cloud brain is already v3-compatible.
- If a configured cloud backend still contains v2-format data, startup detects
  that condition, exits cleanly, and instructs the owner to run
  `gopherbot pull-brain`.
- v2 import/export compatibility logic lives in CLI commands, not in normal
  startup or runtime sync.
- `restore-brain` uploads an existing local cache to a configured cloud backend
  in either v2-compatible or v3 format, enabling smooth dev-file-to-cloud
  transition and rollback.

### Invariant Impact Check

- Startup determinism preserved: yes. Startup either verifies the local cache
  with a bounded point-read, hydrates from an already-v3 remote on first start,
  or exits with a deterministic operator action.
- Explicit control flow preserved: yes. Full remote scans happen only for
  first-start v3 hydration or explicit CLI operations, not as hidden repair
  behavior.
- Shared auth/policy remains in engine flows: yes. Extensions continue to call
  the same Robot memory APIs.
- Permission checks remain username-based: yes. No connector or auth behavior is
  involved.
- Connector ordering guarantees preserved: yes. Connectors are not started until
  brain/cache startup succeeds.
- Config precedence remains explicit: yes. `Brain` selects local-only vs remote
  backend; `BrainCache` controls local cache behavior; provider files keep
  provider-specific remote config.
- Multi-connector isolation preserved: yes. The cache layer is below connector
  fan-in and does not create cross-connector coupling.

### Cross-Cutting Concerns

- Startup sequencing:
  - local cache open and cloud fast-path verification happen before
    `acquireBrainLock()`
  - `bot:instance-lock` must not be hidden behind delayed cloud sync
  - `runBrain()` still starts in `run()` after `initBot()`
- Config:
  - add top-level `BrainCache` config for engine-owned cache settings
  - keep provider credentials in `conf/brains/<provider>.yaml`
  - keep `Brain: file` as local-only with no warning for dev use
- Resource lifecycle:
  - cache opens during `initBot()` and closes during `brainQuit()`
  - cloud sync worker starts with the cached brain and stops on shutdown
  - queued cloud writes are durable on disk, so shutdown need not block forever
    trying to exhaust provider quota
- Concurrency:
  - `runBrain()` already serializes normal memory calls
  - sync worker reads from local outbox and must coordinate with local cache
    metadata using cache-level locking and atomic file replacement
  - no provider should mutate engine state directly

### Backward Compatibility

- Extension API compatibility is preserved. `CheckoutDatum`, `UpdateDatum`,
  `DeleteDatum`, and memory key names continue to behave as before.
- Persistent cloud data compatibility is preserved through explicit
  `pull-brain` import of v2 remote values.
- Config schema compatibility is not guaranteed for v3, but migration should be
  explicit and operator-friendly.
- Existing v2 cloud robots should fail cleanly on first v3 normal startup
  because the remote records are not v3-compatible. The admin then uses CLI
  commands to import the data locally and explicitly write v3 remote records
  before starting the v3 engine.
- A robot owner can later use CLI commands to write the local cache back to
  v2-compatible remote format before reverting to v2 code.
- Existing file brain dev robots should continue to work as local-only brains.
- Existing file brain data should be imported into the cache path only through a
  CLI path. Normal startup should not contain v2/legacy compatibility import
  branches.

### Validation Plan

- Unit tests for local cache key encoding, atomic writes, metadata, tombstones,
  list behavior, outbox coalescing, and quota handling.
- Fake remote backend tests for startup fast path, first-start v3 hydration,
  v2-detected failure, `pull-brain`, `restore-brain`, delete propagation, and
  dirty local outbox replay.
- Focused provider tests with fake HTTP/server clients where practical,
  especially Cloudflare pagination and metadata.
- Existing `go test ./bot` coverage must pass.
- Process-backed integration suites through MCP for memory behavior, at minimum
  `TestMemory`; run broader suites when shared startup/config code changes.
- Docs hygiene check is required for this doc set.
- If core engine or provider runtime code changes, rebuild with `make`.

## Design Decisions

### Local Cache Is The Engine-Facing Brain

The normal engine should not call Cloudflare KV, DynamoDB, or Firestore for
ordinary `Retrieve`, `Store`, `List`, or `Delete` calls. It should call a local
cached brain that has these responsibilities:

- validate memory keys
- store encrypted payload bytes locally
- keep v3 metadata for each key
- write durable outbox entries for cloud mutations
- serve all reads/lists from local state
- run a background sync worker for cloud backends
- provide startup cache verification and CLI sync/import/export operations

This layer should live in or under `bot/`, because encryption, startup,
shutdown, CLI memory commands, and future startup gating are engine-owned.

### File-Only Brain Is Valid

`Brain: file` should remain a supported development mode. It should mean:

- no cloud backend
- local cache is the source of truth
- no sync worker is required
- no cloud verification is required
- no warning should be emitted merely because the robot is local-only

This is distinct from cloud-backed production operation, where local cache is a
cache plus durable write-behind queue for the remote backend.

### Explicit Upgrade CLI Instead Of Startup Compatibility Branches

Normal startup should not import or upgrade v2 remote records. A missing local
cache is allowed when the configured cloud backend is already v3-compatible;
startup should hydrate the local cache from that v3 remote and continue.

If startup sees v2/unversioned remote records and no local cache is available,
it should exit cleanly with instructions like:

```text
Cloud brain "cloudflare" contains v2-format memories.
Run: gopherbot pull-brain
```

The `pull-brain` command is the import compatibility path. It imports v2 data
into the local cache and leaves the cloud untouched by default. A separate
explicit flag can write upgraded v3 records/checkpoints back to the cloud.
Normal startup may detect v2 data so it can refuse to continue, but it must not
import, upgrade, reinterpret, or repair legacy brain state.

This keeps normal startup's compatibility behavior limited to detection and
makes migration an explicit operator action.

### CLI-Only Cloud Format Conversion

The local cache is always v3-format. The v3 runtime remote representation is
also always v3-format for cloud-backed robots.

V2 cloud compatibility is available only through CLI conversion commands:

- `pull-brain` can read v2-compatible cloud records into the local v3 cache
- `pull-brain -upgrade-cloud-v3` can read v2-compatible cloud records and write
  upgraded v3 records/checkpoint back to the cloud
- `restore-brain -remote-format v2` can export the local cache as v2-compatible
  cloud records before the owner reverts to v2 code
- `restore-brain -remote-format v3` can export the local cache as v3 cloud
  records before starting v3 code

The engine-owned cache must not contain v2 remote-format branches in normal
startup or runtime sync. If the v3 runtime sees a v2/unversioned cloud brain, it
should exit with an actionable CLI instruction.

### Simple File Store Layout

Use a directory under the robot state directory:

```text
state/brain-cache/
  control.json
  data/
    <encoded-key>.blob
  meta/
    <encoded-key>.json
  outbox/
    <zero-padded-version>-<operation>-<encoded-key>.json
  legacy-import/
    ...
```

Recommended defaults:

- `BrainCache.Directory`: `${GOPHER_STATE_DIRECTORY:-state}/brain-cache`
- key filename encoding: `base64.RawURLEncoding(key)`
- data files contain encrypted payload bytes directly, not base64
- metadata files are JSON
- writes use temp-file-plus-rename for atomic replacement
- `control.json` tracks schema version, provider identity, next local version,
  cache completeness, last verified v3 checkpoint, and generic sync state

Provider identity should include non-secret backend identity:

- provider name
- Cloudflare account ID + namespace ID
- DynamoDB region + table name
- Firestore project ID + database ID + collection

If the configured provider identity does not match the local cache identity,
startup should exit cleanly and tell the admin which command to run.

### V3 Metadata

Local metadata:

```go
type brainCacheMeta struct {
    Format    string    `json:"format"` // "gopherbot-brain-v3"
    Key       string    `json:"key"`
    Version   uint64    `json:"version"`
    Checksum  string    `json:"checksum"` // sha256 of encrypted payload
    Deleted   bool      `json:"deleted"`
    UpdatedAt time.Time `json:"updated_at"`
    SyncedAt  time.Time `json:"synced_at,omitempty"`
}
```

Remote v3 metadata should carry the same logical fields. Providers may store
that logical metadata as native attributes or as an envelope when native
metadata is not a good fit for the current implementation:

- Cloudflare KV: value is a v3 JSON envelope containing encrypted payload,
  format, version, checksum, deleted, updated_at
- DynamoDB: item has key, content, format, version, checksum, deleted,
  updated_at
- Firestore: document has content, format, version, checksum, deleted,
  updated_at

Deletes should be represented by tombstones long enough to prevent old cloud
data from being rehydrated accidentally. A later compaction command can remove
old tombstones after a configured age.

Remote v2 records have no metadata. For v2-format cloud writes, the value is
only the encrypted payload bytes and deletes physically remove the key.

### Versioning

The local cache owns version assignment. Because the brain instance lock
preserves the single-writer model, a monotonic local `uint64` is sufficient.

- `control.json` stores `NextVersion`
- every store/delete reserves one version before writing local metadata
- outbox entries include the exact version they represent
- runtime sync writes are v3-only and idempotent: remote version lower than local version is
  overwritten; remote version equal with matching checksum is already synced
- remote version greater than local version indicates an unexpected external
  writer or cache mismatch and should stop sync with a loud error

For imported v2 cloud data, `pull-brain` assigns local versions in deterministic
key order after the full listing completes.

### Durable Outbox

Every local mutation creates or replaces an outbox entry. The outbox is required
for Cloudflare budget handling and crash recovery.

Rules:

- a local write is successful only after payload, metadata, and outbox state are
  durable
- repeated writes to the same key should coalesce so only the newest operation
  syncs to cloud
- a delete after a write collapses to a delete tombstone
- a write after a delete collapses to the newest write
- outbox entries remain until remote sync confirms the matching version
- provider quota exhaustion leaves outbox entries on disk for a later cycle

`Shutdown()` should stop accepting new writes, let an in-flight remote write
finish or time out, persist all local state, and stop workers. It must not block
indefinitely trying to flush a quota-limited remote backend.

## Brain Contract Changes

Keep the extension-facing Robot API stable. Change only the provider/engine
contract.

Recommended split:

```go
type SimpleBrain interface {
    Store(key string, blob *[]byte) error
    Retrieve(key string) (blob *[]byte, exists bool, err error)
    List() (keys []string, err error)
    Delete(key string) error
    Shutdown()
}

type RemoteBrainBackend interface {
    Identity() BrainBackendIdentity
    Get(ctx context.Context, key string) (RemoteBrainRecord, bool, error)
    Put(ctx context.Context, record RemoteBrainRecord) error
    Delete(ctx context.Context, tombstone RemoteBrainRecord) error
    ListMetadata(ctx context.Context, cursor string, limit int) (RemoteBrainPage, error)
    SyncPolicy() BrainSyncPolicy
    Shutdown()
}
```

`SimpleBrain` remains what the engine uses. `cachedBrain` implements it.
Cloud providers implement `RemoteBrainBackend`.

`RemoteBrainRecord` should contain encrypted payload bytes plus v3 metadata. For
metadata-only list results, `Payload` can be nil.

`BrainSyncPolicy` is provided by the selected remote backend. It should include
provider-sensitive values such as write budget, minimum write interval,
coalesce window, and flush-on-shutdown bound. This keeps `BrainCache`
provider-neutral when switching between Cloudflare KV, DynamoDB, and Firestore.

Provider registration can either gain a new registry for remote backends or
extend `BrainProviderRegistration` to include provider capabilities. Keep the
call site in `initBot()` explicit:

1. load cache config
2. construct local cache
3. if `Brain == "file"`, construct local-only cached brain
4. otherwise construct remote backend and cached brain wrapper
5. run startup verification or first-start v3 hydration
6. assign `interfaces.brain`

## Configuration Changes And Requirements

### Top-Level Robot Config

`Brain` remains the selector for the logical brain backend:

- `Brain: file` means local-only file cache and is valid for development robots
- `Brain: mem` remains ephemeral and useful for tests/demo behavior
- `Brain: cloudflare`, `Brain: dynamo`, and `Brain: firestore` select a cloud
  backend plus the engine-owned local cache

Add a top-level `BrainCache` section:

```yaml
BrainCache:
  Directory: {{ env "GOPHER_BRAIN_CACHE_DIRECTORY" | default "<GOPHER_STATE_DIRECTORY-or-state>/brain-cache" }}
  RequireRemoteCleanOnStartup: false
```

Required semantics:

- `Directory` is the v3 local cache location for both local-only and
  cloud-backed brains.
- Runtime remote format is always v3 for cloud-backed brains; there is no
  `RemoteFormat` in engine config.
- `RequireRemoteCleanOnStartup` defaults to `false`; when true, cloud-backed
  startup can require the local outbox to drain before readiness.
- Provider-sensitive sync values are not configured in `BrainCache`. The cache
  asks the selected remote backend for sync policy values, so changing from
  Cloudflare KV to DynamoDB or Firestore does not require cache config changes.

### Provider Config Files

Provider credentials and non-secret provider identity remain in
`conf/brains/<provider>.yaml`.

Cloudflare:

- keep `AccountID`, `NamespaceID`, and `APIToken`
- remove or ignore `MaxAgeHours`; provider-local in-memory cache eviction goes
  away because caching is engine-owned
- add optional provider-owned sync policy fields such as write budget, minimum
  write interval, coalesce window, and flush-on-shutdown bound; defaults should
  be conservative for Cloudflare KV free-tier usage
- `AccountID` + `NamespaceID` are part of cache provider identity

DynamoDB:

- keep `TableName`, `Region`, `AccessKeyID`, and `SecretAccessKey`
- add optional provider-owned sync policy fields only if needed; defaults can
  be much less restrictive than Cloudflare KV
- `Region` + `TableName` are part of cache provider identity

Firestore:

- keep `ProjectID`, `DatabaseID`, `Collection`, `CredentialsEncryptedFile`, and
  `OperationTimeoutSeconds`
- add optional provider-owned sync policy fields only if needed; defaults can
  be much less restrictive than Cloudflare KV
- resolved `ProjectID` + `DatabaseID` + `Collection` are part of cache provider
  identity

File brain:

- new runtime should use `BrainCache.Directory`
- existing `BrainConfig.BrainDirectory` is legacy import input for
  `gopherbot pull-brain` only
- normal startup should not read old v2 file-brain data from
  `BrainConfig.BrainDirectory`

### CLI Format Overrides

`gopherbot pull-brain -upgrade-cloud-v3` explicitly upgrades remote cloud
records to v3 format.

`gopherbot restore-brain -remote-format v2|v3` explicitly chooses the remote
format to write from local cache.

When no CLI format flag is supplied, commands should avoid modifying the remote
backend if doing so would be ambiguous or could cause an accidental one-way cloud
upgrade. The CLI should exit with an actionable message asking for an explicit
`-remote-format` or `-upgrade-cloud-v3`.

## Startup Behavior

### Local-Only File Brain

When `Brain: file`:

1. open/create local cache
2. if old file-brain data is detected outside the v3 cache layout, exit cleanly
   with instructions to run `gopherbot pull-brain`
3. mark cache ready
4. assign `interfaces.brain`
5. continue startup

No cloud reads, cloud writes, warnings, or migration errors are needed.
The only clean-start failure for local-only file mode is legacy/v2 file data in
a non-v3 layout; that is an actionable migration stop, not a warning about
using local-only storage.

### Cloud-Backed Steady-State Startup

When `Brain` is a cloud backend and a complete local cache exists:

1. open local cache
2. verify local provider identity matches current config
3. verify cache completeness flag is true
4. verify there is a v3 checkpoint key in local control state
5. point-read that checkpoint key from the cloud backend and compare remote
   format/version/checksum/deleted with local metadata
6. start sync worker using provider-owned sync policy
7. assign `interfaces.brain`
8. continue startup

This checkpoint read is the only cloud read required in the normal healthy case.
The v3 engine requires the configured cloud backend to be v3-compatible.

If there are durable outbox entries, the cache can still be considered locally
ready as long as provider identity matches and the cache was previously
complete. The sync worker resumes queued writes. Do not perform a full scan just
because local cloud writes remain queued; that would punish CFKV and defeat the
cache purpose.

Optional future config can force remote-clean startup for operators who prefer
blocking until the outbox drains:

```yaml
BrainCache:
  RequireRemoteCleanOnStartup: false
```

Default should be `false`.

### Cloud-Backed First-Start Hydration

When `Brain` is a cloud backend and no local cache exists, startup should treat
this as the normal first-start path for a new VM:

1. construct the remote backend
2. list remote metadata with provider pagination
3. if any remote memory is v2/unversioned, exit cleanly and instruct the owner
   to run `gopherbot pull-brain`
4. if the remote is empty, initialize an empty v3 local cache and continue
5. if all remote memories are v3-compatible, fetch payloads as needed and write
   the complete local cache
6. write local control/checkpoint state
7. start the sync worker
8. assign `interfaces.brain`
9. continue startup

This full remote scan is acceptable because it happens only when the local cache
does not exist. It is not a v2 compatibility path; it hydrates only
v3-compatible remote data. V2/unversioned remote data still requires
`pull-brain`, because v2 import compatibility belongs only in CLI paths.

### Cloud-Backed Failure Cases

Startup should exit cleanly, before connectors start, when:

- cache schema is unsupported
- cache provider identity does not match config
- cache completeness flag is false
- local checkpoint is missing
- remote checkpoint is missing
- remote checkpoint is v2/unversioned
- remote checkpoint version/checksum does not match local state
- first-start hydration discovers any v2/unversioned remote memory
- remote backend reports a hard auth/config error

Error messages should name the provider and the recommended command:

- `gopherbot pull-brain` for v2 remote data
- `gopherbot restore-brain` when a local-only cache exists and the operator has
  just configured a new empty cloud backend

### Future Startup Gate Hook

The later startup-gating story should use a separate `startingUp` state, not
`state.shuttingDown`.

Placement:

- keep `IgnoreUsers` and `IgnoreUnlistedUsers` as first filters in
  `handler.IncomingMessage`
- after user filtering and command detection, but before worker creation, reply
  to commands with:

```text
Sorry, I'm still starting up, please wait and try your command again later
```

This hook should not be required for the normal cloud-backed fast path. It is
for bounded startup work such as first initialization and any intentionally
blocking cache sync mode.

## CLI Commands

### `pull-brain`

Purpose: initialize or refresh the local cache from the configured brain
backend. This is the explicit v2 cloud import path.

All v2 and legacy file-brain compatibility code belongs here. Normal startup and
normal runtime must not import, upgrade, reinterpret, or repair legacy brain
state.

Behavior:

1. run config/encryption initialization only; do not start connectors/plugins
2. if `Brain: file`, read the configured legacy file brain directory when one
   exists and import it into the v3 local cache layout
3. otherwise construct the configured remote backend directly
4. open/create local cache
5. list all remote metadata with provider pagination
6. detect each remote record as v2 or v3
7. fetch payloads for missing/outdated local records
8. write local payload/metadata
9. assign local versions for v2 imports after deterministic ordering
10. record whether the local cache was imported from v2 or v3 remote data for
    operator diagnostics only
11. write local completeness flag and local checkpoint
12. if the owner supplied the v3 cloud-upgrade flag, write upgraded v3 records
    and a v3 checkpoint back to the remote backend, subject to
    write-budget/resume handling

Recommended flags:

```text
gopherbot pull-brain
gopherbot pull-brain -force
gopherbot pull-brain -dry-run
gopherbot pull-brain -upgrade-cloud-v3
gopherbot pull-brain -budget <writes>
```

Recommended default: `pull-brain` should create a usable local v3 cache and
should not modify the cloud backend. `-upgrade-cloud-v3` is the explicit opt-in
that writes upgraded v3 records and checkpoint metadata to the cloud. If the
configured write budget prevents finishing that optional cloud upgrade in one
run, the command should leave resumable progress and report that the v3 runtime
must not be started against that cloud brain until the v3 cloud upgrade or
`restore-brain -remote-format v3` completes.

Provider details:

- Cloudflare KV listing must follow cursors and decode v3 envelopes for
  metadata. Missing or undecodable envelopes are treated as v2/unversioned
  records by startup detection and CLI import.
- DynamoDB v2 detection is missing format/version/checksum attributes.
- Firestore v2 detection is missing format/version/checksum fields.

If `-dry-run` is set, print counts:

- total remote keys
- v2 keys
- v3 keys
- keys needing payload download
- keys needing local update
- cloud writes required for checkpoint/backfill
- whether the command would modify the remote backend

### `restore-brain`

Purpose: write the local cache to the configured cloud backend. This supports
the flow where a development robot starts as `Brain: file`, then later gets a
Cloudflare/DynamoDB/Firestore brain. It is also the deliberate rollback/export
path because it can write either v2-compatible or v3 cloud records.

Behavior:

1. run config/encryption initialization only
2. open local cache
3. construct configured remote backend
4. verify provider identity; if this is a new remote, initialize identity after
   operator confirmation or `-force`
5. list local non-deleted records and tombstones
6. write records to cloud using the requested remote format
7. for remote format `v3`, write v3 metadata/checkpoint records
8. for remote format `v2`, write only v2-compatible encrypted values and apply
   tombstones as physical deletes
9. update local synced state when writing v3 format; when writing v2 format,
   report that the export is intended for rollback and is not a valid v3 runtime
   remote brain

Recommended flags:

```text
gopherbot restore-brain
gopherbot restore-brain -dry-run
gopherbot restore-brain -force
gopherbot restore-brain -remote-format v2
gopherbot restore-brain -remote-format v3
gopherbot restore-brain -budget <writes>
```

`restore-brain` should require an explicit `-remote-format` unless the command
can infer a safe default from context without modifying the remote unexpectedly.
This avoids accidental one-way upgrades.

`restore-brain` must respect provider write budgets. If it cannot finish in one
run, it should leave clear progress information and be safely resumable.

### Existing Memory CLI Commands

After this change:

- `fetch`, `store`, `list`, and `delete` should operate on the local cache
- for cloud-backed brains, `store` and `delete` enqueue cloud sync like runtime
  writes
- do not add implicit remote reads to `fetch`
- optional future flags such as `list -cloud` should be separate, explicit
  provider inspection commands

## Cloud Sync Worker

The sync worker should be owned by `cachedBrain`.

Responsibilities:

- load durable outbox on startup
- coalesce operations per key before writing
- ask the remote backend for provider-specific sync policy
- apply provider rate limits and budgets from that sync policy
- write v3 remote records, metadata/checkpoints, and tombstones
- verify returned or reread metadata when needed
- mark local metadata as synced
- remove outbox entries after confirmation
- update checkpoint after the newest synced version
- expose status for logs and future admin/CLI inspection

Provider sync policy defaults:

- Cloudflare KV:
  - conservative write budget, default 900/day
  - global write pacing at least 1 second
  - coalescing enabled
  - cursor pagination for list operations
  - runtime writes are v3 value-plus-metadata writes
  - CLI conversion helpers can write v2-compatible values
- DynamoDB:
  - no daily budget by default
  - batch/transaction support where useful
  - strong point reads for checkpoint verification
  - runtime writes use the v3 item shape
  - CLI conversion helpers can write the v2-compatible item shape
- Firestore:
  - no daily budget by default
  - batch writes where useful
  - projection query for metadata scan in `pull-brain`
  - runtime writes use the v3 document shape
  - CLI conversion helpers can write the v2-compatible document shape

## Special Internal Keys

`bot:instance-lock` must not behave like ordinary delayed-sync memory.

Recommended handling:

- for local-only `Brain: file`, store lock locally as today
- for cloud-backed brains, use a write-through or remote-bypass path for lock
  acquire/release
- the write-through path for cloud-backed v3 runtime writes v3-compatible remote
  records
- do not let ordinary outbox delay hide a held lock from another process
- document this exception in `aidocs/STARTUP_FLOW.md`

If the lock remains encrypted, the existing `getDatum()` / `storeDatum()`
encryption path can still be used, but the cached brain should expose an
internal write-through operation for this key.

## Provider Implementation Notes

### Cloudflare KV

The current Cloudflare provider has an in-memory cache and write queue. Replace
that with the engine-owned cache/outbox.

Needed changes:

- implement `RemoteBrainBackend`
- remove provider-local memory cache/janitor
- support paginated key listing with metadata
- support v3 metadata/checkpoint writes for runtime
- expose provider-specific sync policy, with conservative defaults for
  Cloudflare KV
- distinguish hard auth/config errors from transient HTTP failures
- honor rate limits and budget from the cached brain worker

Cloudflare v3 values are JSON envelopes holding encrypted payload plus v3
logical metadata. This is less efficient during metadata scans than native KV
metadata, but it avoids hidden compatibility complexity and keeps v2 values
easy to detect. CLI conversion helpers may separately write v2-compatible raw
values without metadata.

### DynamoDB

Current item:

```go
type dynaMemory struct {
    Memory  string
    Content []byte
}
```

Add v3 fields:

```go
Format string
Version uint64
Checksum string
Deleted bool
UpdatedAt string
```

`ListMetadata` should scan only projected metadata fields, not full payloads.
`Get` should use a consistent read for checkpoint verification.
CLI conversion helpers may separately write the current v2 item shape with
`Memory` and `Content` only.

### Firestore

Current document:

```go
type storedMemory struct {
    Content []byte `firestore:"content"`
}
```

Add v3 fields:

```go
Format string `firestore:"format"`
Version uint64 `firestore:"version"`
Checksum string `firestore:"checksum"`
Deleted bool `firestore:"deleted"`
UpdatedAt time.Time `firestore:"updated_at"`
```

`ListMetadata` should use a projection query where possible so full payloads are
not downloaded during `pull-brain` diffing.
CLI conversion helpers may separately write the current v2 document shape with
`content` only.

## Legacy File Brain Migration

The existing file brain stores one file per key under `BrainDirectory`, with
optional base64 encoding.

Recommended behavior:

- normal startup should not read old v2 file-brain data
- `pull-brain` should support a local-only import path for `Brain: file`
- the import should be idempotent
- after import, all new writes go through the v3 cache layout

This path is for local/dev convenience and should not emit scary migration
warnings.

## Documentation Updates For Implementation

When coding this plan, update:

- `aidocs/STARTUP_FLOW.md`
  - brain cache open/verification
  - cloud startup failure modes
  - `pull-brain` and `restore-brain` CLI startup behavior
  - `bot:instance-lock` write-through exception
- `aidocs/COMPONENT_MAP.md`
  - local cache files
  - remote backend files
  - CLI command anchors
- `aidocs/V3_COMPATIBILITY_CONTRACT.md`
  - explicit brain migration stance
  - cloud v2 import through `pull-brain`
  - required v3 remote brain for the v3 runtime
  - CLI-only v3 upgrade and v2 rollback/export paths
  - file-only dev brain support
- `UPGRADING-v3.md`
  - operator steps for cloud-backed v2 robots
  - operator steps for optional cloud v3 upgrade
  - operator steps for writing v2-compatible cloud data for rollback
  - operator steps for dev file-to-cloud transition
- `conf/README.md` or provider defaults if brain config guidance changes
- `aidocs/TESTING_CURRENT.md` if new process-backed suites or CLI verification
  paths are added

Run `helpers/check-docs-hygiene.sh` for any doc changes.

## Suggested Implementation Slices

### Slice 1: Config And Interfaces

- Add `BrainCache` config to `ConfigLoader`.
- Add processed cache config to `configuration`.
- Add remote backend types and registration path.
- Add provider-owned sync policy returned by remote backends.
- Keep `SimpleBrain` stable for the engine.
- Add fake remote backend for tests.
- Update docs for the new config shape.

Validation:

- config load tests for defaults and overrides
- provider registration tests
- `go test ./bot`

### Slice 2: Local File Cache

- Implement cache directory layout.
- Implement key encoding.
- Implement atomic payload/meta/control writes.
- Implement local `Store`, `Retrieve`, `List`, `Delete`.
- Implement tombstones.

Validation:

- local cache unit tests
- existing memory unit tests
- `TestMemory` process-backed suite

### Slice 3: Cached Brain Wrapper And Startup Fast Path

- Implement `cachedBrain`.
- Wire `initBot()` to build local-only or cloud-backed cached brain.
- Add startup verification for cloud backends.
- Add first-start hydration from already-v3 cloud backends.
- Make clean startup failure messages actionable.
- Keep `runBrain()` semantics unchanged.

Validation:

- fake backend startup tests
- first-start v3 hydration tests
- cloud v2/checkpoint mismatch tests
- `go test ./bot`

### Slice 4: Outbox And Sync Worker

- Implement durable outbox.
- Add coalescing.
- Add write budget/rate limiter driven by remote backend sync policy.
- Add checkpoint update.
- Make runtime sync writes v3-only.
- Adjust `Shutdown()` semantics for durable queued writes.

Validation:

- crash/restart simulation tests using fake backend
- budget exhaustion tests
- delete/write coalescing tests

### Slice 5: Cloud Backend Conversion

- Convert Cloudflare KV to `RemoteBrainBackend`.
- Convert DynamoDB to `RemoteBrainBackend`.
- Convert Firestore to `RemoteBrainBackend`.
- Add v2/v3 detection in backend record decoding.
- Add v3 runtime write paths for each cloud backend.
- Add CLI-only v2-compatible import/export helpers for each cloud backend.
- Remove Cloudflare provider-local memory cache/queue.

Validation:

- provider unit tests with fake HTTP or stubbed clients
- manual dry-run against test cloud resources if available
- targeted process-backed memory suite with fake/local backend where practical

### Slice 6: CLI Migration Commands

- Add `pull-brain`.
- Add `restore-brain`.
- Implement all v2 cloud and legacy file-brain compatibility in these CLI paths
  only.
- Make `pull-brain` default to local import without remote modification.
- Add explicit `pull-brain -upgrade-cloud-v3`.
- Add `restore-brain -remote-format v2|v3`.
- Update existing memory CLI commands to use the local cache.
- Add dry-run and resumability behavior.

Validation:

- CLI unit tests
- temp-dir local file brain import tests
- fake remote v2 and v3 pull tests
- fake remote restore tests for both v2 and v3 output formats

### Slice 7: Documentation And Operator Workflow

- Update all docs listed above.
- Add `UPGRADING-v3.md` steps.
- Add examples for:
  - existing cloud v2 robot: run `pull-brain`, then
    `restore-brain -remote-format v3` or `pull-brain -upgrade-cloud-v3`, then
    start v3
  - dev file-only robot: keep `Brain: file`
  - dev-to-cloud robot: configure cloud backend, run `restore-brain
    -remote-format v3`, then start
  - rollback/export: run `restore-brain -remote-format v2`, then restart with
    v2 code if needed

Validation:

- `helpers/check-docs-hygiene.sh`
- `make`
- applicable process-backed integration suite through MCP

## Acceptance Criteria

- `Brain: file` starts and persists locally with no cloud configuration.
- A cloud-backed robot with a valid local cache performs one v3 cloud checkpoint
  point-read on normal startup and then serves reads locally.
- A cloud-backed robot without a local cache hydrates from an already
  v3-compatible remote brain and then continues startup.
- A cloud-backed robot without a local cache exits cleanly only when remote
  memories are v2/unversioned, and tells the admin to run `pull-brain`.
- A v2 cloud brain can be imported with `pull-brain` without modifying cloud
  data by default; this does not by itself make the cloud brain valid for v3
  runtime startup.
- `pull-brain -upgrade-cloud-v3` writes upgraded v3 records/checkpoint back to
  the cloud.
- A local-only dev brain can be uploaded with `restore-brain`.
- `restore-brain -remote-format v2` writes v2-compatible cloud data suitable
  for rollback to v2 code.
- `restore-brain -remote-format v3` writes the v3 cloud data required before
  starting the v3 engine against that cloud brain.
- Runtime writes return after durable local commit, not after cloud write.
- Cloud writes are coalesced, metered, resumable, and durable across crashes.
- Deletes do not resurrect after pull/restart/sync.
- `bot:instance-lock` remains effective for cloud-backed brains.
- Existing extension memory APIs behave the same from plugin/job/task code.
- Relevant docs and upgrade guide are updated in the same implementation branch.
