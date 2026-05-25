package robot

import (
	"context"
	"time"
)

// Logger is used in various modules for logging errors
type Logger interface {
	Log(l LogLevel, m string, v ...interface{}) bool
}

// SimpleBrain is the simple interface for a configured brain, where the robot
// handles all locking issues.
type SimpleBrain interface {
	// Store stores a blob of data with a string key, returns error
	// if there's a problem storing the datum.
	Store(key string, blob *[]byte) error
	// Retrieve returns a blob of data (probably JSON) given a string key,
	// and exists=true if the data blob was found, or error if the brain
	// malfunctions.
	Retrieve(key string) (blob *[]byte, exists bool, err error)
	// List returns a list of all memories - Gopherbot isn't a database,
	// so it _should_ be pretty short.
	List() (keys []string, err error)
	// Delete deletes a memory
	Delete(key string) error
	// Flush blocks until all delayed writes are durably committed to the
	// backing provider.
	Flush() error
	// Shutdown should stop any goroutines (if any). Callers should call Flush
	// first when they need a clean shutdown or restart.
	Shutdown()
}

type BrainBackendIdentity struct {
	Provider string `json:"provider"`
	Scope    string `json:"scope"`
}

type BrainSyncPolicy struct {
	WriteBudgetPerDay          int
	MinWriteInterval           time.Duration
	CoalesceWindow             time.Duration
	FlushOnShutdownMaxDuration time.Duration
	CheckpointVerifyRetries    int
	CheckpointVerifyDelay      time.Duration
}

type RemoteBrainRecord struct {
	Key       string
	Payload   []byte
	Format    string
	Version   uint64
	Checksum  string
	Deleted   bool
	UpdatedAt time.Time
}

type RemoteBrainPage struct {
	Records    []RemoteBrainRecord
	NextCursor string
}

// RemoteBrainBackend is the v3 remote-provider contract used by the
// engine-owned cached brain. It deals in encrypted payload bytes plus v3
// metadata.
type RemoteBrainBackend interface {
	Identity() BrainBackendIdentity
	Get(ctx context.Context, key string) (RemoteBrainRecord, bool, error)
	Put(ctx context.Context, record RemoteBrainRecord) error
	Delete(ctx context.Context, tombstone RemoteBrainRecord) error
	ListMetadata(ctx context.Context, cursor string, limit int) (RemoteBrainPage, error)
	SyncPolicy() BrainSyncPolicy
	Shutdown()
}

type LegacyBrainRecord struct {
	Key     string
	Payload []byte
}

type LegacyBrainPage struct {
	Records    []LegacyBrainRecord
	NextCursor string
}

// LegacyBrainBackend is for CLI-only v2 import/export paths. The v3 runtime
// must not use this interface.
type LegacyBrainBackend interface {
	ListV2(ctx context.Context, cursor string, limit int) (LegacyBrainPage, error)
	GetV2(ctx context.Context, key string) (LegacyBrainRecord, bool, error)
	PutV2(ctx context.Context, record LegacyBrainRecord) error
	DeleteV2(ctx context.Context, key string) error
}
