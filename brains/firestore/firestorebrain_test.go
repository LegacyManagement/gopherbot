package firestorebrain

import (
	"testing"
	"time"

	"github.com/lnxjedi/gopherbot/robot"
)

func TestFirestoreBrainVersionConversionUsesSignedInteger(t *testing.T) {
	updatedAt := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	record := robot.RemoteBrainRecord{
		Key:       "plugin:key",
		Payload:   []byte("payload"),
		Format:    brainCacheFormat,
		Version:   42,
		Checksum:  "abc123",
		Deleted:   true,
		UpdatedAt: updatedAt,
	}

	stored, err := storedMemoryFromRemoteRecord(record)
	if err != nil {
		t.Fatalf("storedMemoryFromRemoteRecord() error = %v", err)
	}
	if stored.Version != 42 {
		t.Fatalf("stored version = %d, want 42", stored.Version)
	}

	roundTrip, err := remoteRecordFromStoredMemory(record.Key, stored)
	if err != nil {
		t.Fatalf("remoteRecordFromStoredMemory() error = %v", err)
	}
	if roundTrip.Version != record.Version {
		t.Fatalf("round-trip version = %d, want %d", roundTrip.Version, record.Version)
	}
	if roundTrip.Checksum != record.Checksum || roundTrip.Deleted != record.Deleted || !roundTrip.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("round-trip record = %+v, want metadata from %+v", roundTrip, record)
	}
}

func TestFirestoreBrainVersionConversionRejectsOverflow(t *testing.T) {
	_, err := storedMemoryFromRemoteRecord(robot.RemoteBrainRecord{
		Key:     "plugin:key",
		Version: maxFirestoreBrainVersion + 1,
	})
	if err == nil {
		t.Fatal("storedMemoryFromRemoteRecord() accepted uint64 value larger than Firestore can store")
	}
}

func TestFirestoreBrainVersionConversionRejectsNegativeStoredVersion(t *testing.T) {
	_, err := remoteRecordFromStoredMemory("plugin:key", storedMemory{Version: -1})
	if err == nil {
		t.Fatal("remoteRecordFromStoredMemory() accepted negative Firestore version")
	}
}
