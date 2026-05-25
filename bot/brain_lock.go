package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/lnxjedi/gopherbot/robot"
)

const brainLockKey = "bot:instance-lock"

const (
	brainLockHeld     = "held"
	brainLockReleased = "released"
)

// instanceLockData captures identifying information about the running robot
// instance stored in the brain lock. This mirrors the data surfaced by the
// "info" admin command so that an operator can identify which instance holds
// the lock.
type instanceLockData struct {
	State            string `json:"state,omitempty"`
	LockID           string `json:"lock_id,omitempty"`
	CacheNonceHash   string `json:"cache_nonce_hash,omitempty"`
	RobotName        string `json:"robot_name"`
	FullName         string `json:"full_name,omitempty"`
	Hostname         string `json:"hostname"`
	PID              int    `json:"pid"`
	StartMode        string `json:"start_mode"`
	InstallPath      string `json:"install_path"`
	HomePath         string `json:"home_path"`
	ConfigPath       string `json:"config_path"`
	CustomRepository string `json:"custom_repository,omitempty"`
	Version          string `json:"version"`
	Commit           string `json:"commit"`
	StartTime        string `json:"start_time,omitempty"`
	AcquiredAt       string `json:"acquired_at,omitempty"`
	ReleasedAt       string `json:"released_at,omitempty"`
	DatabaseVersion  uint64 `json:"database_version,omitempty"`
}

// acquireBrainLock checks for an existing instance lock in the brain and
// creates one if absent. It is called during initBot() after brain and
// encryption initialization, and only for non-CLI robot startup.
//
// If a lock is already present, the robot logs identifying information about
// the holding instance and calls Log(Fatal,...) to abort startup.
func acquireBrainLock() {
	lock, exists, err := readBrainLockForStartup()
	if err != nil {
		Log(robot.Fatal, "Unable to check brain instance lock: %v", err)
		return
	}
	if exists {
		state := lock.State
		if state == "" {
			state = brainLockHeld
		}
		switch state {
		case brainLockHeld:
			if canReclaimHeldBrainLock(lock) {
				Log(robot.Warn, "Reclaiming held brain lock from previous local process lock_id=%s", lock.LockID)
			} else {
				Log(robot.Fatal, "%s", formatHeldBrainLockMessage(lock))
				return
			}
		case brainLockReleased:
			if err := validateReleasedBrainLock(lock); err != nil {
				Log(robot.Fatal, "%v", err)
				return
			}
		default:
			Log(robot.Fatal, "Brain instance lock has unknown state %q; refusing startup", state)
			return
		}
	}
	if err := verifyBrainCacheLastCloudWrite(); err != nil {
		Log(robot.Fatal, "Brain cache last cloud write verification failed: %v", err)
		return
	}
	if err := writeBrainLock(brainLockHeld); err != nil {
		Log(robot.Fatal, "Unable to acquire brain instance lock: %v", err)
		return
	}
	replayBrainCacheOutboxOnStartup()
}

func newInstanceLockData(stateName, lockID string, databaseVersion uint64) instanceLockData {
	customRepo, _ := lookupEnv("GOPHER_CUSTOM_REPOSITORY")
	now := time.Now().Format(time.RFC3339)
	lock := instanceLockData{
		State:            stateName,
		LockID:           lockID,
		CacheNonceHash:   currentBrainCacheNonceHash(),
		RobotName:        currentCfg.botinfo.UserName,
		FullName:         currentCfg.botinfo.FullName,
		Hostname:         hostName,
		PID:              os.Getpid(),
		StartMode:        startMode,
		InstallPath:      installPath,
		HomePath:         homePath,
		ConfigPath:       configFull,
		CustomRepository: customRepo,
		Version:          botVersion.Version,
		Commit:           botVersion.Commit,
		DatabaseVersion:  databaseVersion,
	}
	if stateName == brainLockReleased {
		lock.ReleasedAt = now
	} else {
		lock.StartTime = now
		lock.AcquiredAt = now
	}
	return lock
}

func writeBrainLock(stateName string) error {
	lockID := currentBrainLockID()
	if stateName == brainLockHeld {
		lockID = randomBrainCacheID()
		if err := setCurrentBrainLockID(lockID); err != nil {
			return fmt.Errorf("persisting lock id: %w", err)
		}
	}
	lock := newInstanceLockData(stateName, lockID, currentBrainDatabaseVersion())

	data, err := json.Marshal(lock)
	if err != nil {
		return fmt.Errorf("marshalling lock data: %w", err)
	}
	if ret := storeDatum(brainLockKey, &data); ret != robot.Ok {
		return fmt.Errorf("storing %s returned %s", brainLockKey, ret)
	}
	if stateName == brainLockReleased {
		Log(robot.Debug, "Brain instance lock released (%s)", brainLockKey)
	} else {
		Log(robot.Debug, "Brain instance lock acquired (%s)", brainLockKey)
	}
	return nil
}

func readBrainLockForStartup() (instanceLockData, bool, error) {
	if cb, ok := interfaces.brain.(*cachedBrain); ok && cb.remote != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		record, exists, err := cb.remote.Get(ctx, brainLockKey)
		if err != nil || !exists || record.Deleted {
			return instanceLockData{}, exists && !record.Deleted, err
		}
		plain, err := decryptMemoryPayload(record.Payload)
		if err != nil {
			return instanceLockData{}, true, err
		}
		var lock instanceLockData
		if err := json.Unmarshal(plain, &lock); err != nil {
			return instanceLockData{}, true, err
		}
		return lock, true, nil
	}
	_, existing, exists, ret := getDatum(brainLockKey, false)
	if ret != robot.Ok {
		return instanceLockData{}, false, fmt.Errorf("ret=%s", ret)
	}
	if !exists {
		return instanceLockData{}, false, nil
	}
	if existing == nil {
		return instanceLockData{}, true, fmt.Errorf("brain instance lock exists with empty data")
	}
	var lock instanceLockData
	if err := json.Unmarshal(*existing, &lock); err != nil {
		return instanceLockData{}, true, err
	}
	return lock, true, nil
}

func canReclaimHeldBrainLock(lock instanceLockData) bool {
	active := currentBrainLockID()
	if active == "" || lock.LockID == "" || lock.LockID != active {
		return false
	}
	expectedHash := currentBrainCacheNonceHash()
	return expectedHash != "" && lock.CacheNonceHash == expectedHash
}

func validateReleasedBrainLock(lock instanceLockData) error {
	localVersion := currentBrainDatabaseVersion()
	if lock.DatabaseVersion > localVersion {
		return fmt.Errorf("brain instance lock was released at database version %d, but local cache only knows version %d; run gopherbot pull-brain or choose the correct BrainCache.Directory", lock.DatabaseVersion, localVersion)
	}
	return nil
}

func formatHeldBrainLockMessage(lock instanceLockData) string {
	started := lock.StartTime
	if started == "" {
		started = lock.AcquiredAt
	}
	return fmt.Sprintf(
		"Brain instance lock held by another robot instance:\n"+
			"  Robot:   %s (%s)\n"+
			"  Host:    %s  PID: %d\n"+
			"  Mode:    %s  Started: %s\n"+
			"  Version: Gopherbot %s commit %s\n"+
			"  Home:    %s\n"+
			"  Config:  %s\n"+
			"If this lock is stale, inspect it with: gopherbot fetch -cloud %s",
		lock.RobotName, lock.FullName,
		lock.Hostname, lock.PID,
		lock.StartMode, started,
		lock.Version, lock.Commit,
		lock.HomePath,
		lock.ConfigPath,
		brainLockKey,
	)
}

func verifyBrainCacheLastCloudWrite() error {
	if cb, ok := interfaces.brain.(*cachedBrain); ok {
		return cb.verifyLastCloudWrite()
	}
	return nil
}

func replayBrainCacheOutboxOnStartup() {
	cb, ok := interfaces.brain.(*cachedBrain)
	if !ok || cb.remote == nil {
		return
	}
	pending, err := cb.outboxEntries()
	if err != nil {
		Log(robot.Fatal, "Reading brain cache outbox during startup: %v", err)
		return
	}
	if len(pending) == 0 {
		return
	}
	Log(robot.Warn, "Brain cache startup replay: syncing %d pending cloud write(s) before readiness", len(pending))
	if err := cb.Flush(); err != nil {
		Log(robot.Fatal, "Brain cache startup replay failed: %v", err)
	}
}

func currentBrainDatabaseVersion() uint64 {
	if cb, ok := interfaces.brain.(*cachedBrain); ok {
		return cb.latestDatabaseVersion()
	}
	return 0
}

func currentBrainCacheNonceHash() string {
	if cb, ok := interfaces.brain.(*cachedBrain); ok {
		return cb.cacheNonceHash()
	}
	return ""
}

func currentBrainLockID() string {
	if cb, ok := interfaces.brain.(*cachedBrain); ok {
		return cb.activeLockID()
	}
	return ""
}

func setCurrentBrainLockID(lockID string) error {
	if cb, ok := interfaces.brain.(*cachedBrain); ok {
		return cb.setActiveLockID(lockID)
	}
	return nil
}

// releaseBrainLock writes the instance lock as released with the local database
// version. It is called during stop() after running pipelines complete and
// before brainQuit().
func releaseBrainLock() {
	brain := interfaces.brain
	if brain == nil {
		return
	}
	if err := writeBrainLock(brainLockReleased); err != nil {
		Log(robot.Warn, "Unable to release brain instance lock: %v", err)
	}
}
