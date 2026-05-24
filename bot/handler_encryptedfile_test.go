package bot

import (
	"os"
	"path/filepath"
	"testing"
)

func setCryptKeyForEncryptedFileTest(t *testing.T, key []byte) {
	t.Helper()
	cryptKey.Lock()
	oldKey := append([]byte(nil), cryptKey.key...)
	oldInitialized := cryptKey.initialized
	oldInitializing := cryptKey.initializing
	cryptKey.key = append([]byte(nil), key...)
	cryptKey.initialized = true
	cryptKey.initializing = false
	cryptKey.Unlock()
	t.Cleanup(func() {
		cryptKey.Lock()
		cryptKey.key = oldKey
		cryptKey.initialized = oldInitialized
		cryptKey.initializing = oldInitializing
		cryptKey.Unlock()
	})
}

func TestHandlerReadEncryptedFileUsesConfigRoot(t *testing.T) {
	oldConfigFull := configFull
	oldInstallPath := installPath
	configFull = t.TempDir()
	installPath = t.TempDir()
	testKey := []byte("12345678901234567890123456789012")
	setCryptKeyForEncryptedFileTest(t, testKey)
	t.Cleanup(func() {
		configFull = oldConfigFull
		installPath = oldInstallPath
	})

	plaintext := []byte("{\"type\":\"service_account\"}\n")
	ciphertext, err := encrypt(plaintext, testKey)
	if err != nil {
		t.Fatalf("encrypt() error: %v", err)
	}
	if err := WriteBase64File(filepath.Join(configFull, "gopherbot-key.json.enc"), &ciphertext); err != nil {
		t.Fatalf("WriteBase64File(): %v", err)
	}

	got, err := (handler{}).ReadEncryptedFile("gopherbot-key.json.enc")
	if err != nil {
		t.Fatalf("ReadEncryptedFile() error: %v", err)
	}
	if string(got) != string(plaintext) {
		t.Fatalf("ReadEncryptedFile() = %q, want %q", string(got), string(plaintext))
	}
}

func TestHandlerReadEncryptedFileFallsBackToInstallRoot(t *testing.T) {
	oldConfigFull := configFull
	oldInstallPath := installPath
	configFull = t.TempDir()
	installPath = t.TempDir()
	testKey := []byte("12345678901234567890123456789012")
	setCryptKeyForEncryptedFileTest(t, testKey)
	t.Cleanup(func() {
		configFull = oldConfigFull
		installPath = oldInstallPath
	})

	plaintext := []byte("install-root-secret")
	ciphertext, err := encrypt(plaintext, testKey)
	if err != nil {
		t.Fatalf("encrypt() error: %v", err)
	}
	if err := WriteBase64File(filepath.Join(installPath, "shared.enc"), &ciphertext); err != nil {
		t.Fatalf("WriteBase64File(): %v", err)
	}

	got, err := (handler{}).ReadEncryptedFile("shared.enc")
	if err != nil {
		t.Fatalf("ReadEncryptedFile() error: %v", err)
	}
	if string(got) != string(plaintext) {
		t.Fatalf("ReadEncryptedFile() = %q, want %q", string(got), string(plaintext))
	}
}

func TestHandlerReadEncryptedFileRejectsTraversalOutsideRoots(t *testing.T) {
	oldConfigFull := configFull
	oldInstallPath := installPath
	configFull = t.TempDir()
	installPath = t.TempDir()
	setCryptKeyForEncryptedFileTest(t, []byte("12345678901234567890123456789012"))
	t.Cleanup(func() {
		configFull = oldConfigFull
		installPath = oldInstallPath
	})

	outsideDir := t.TempDir()
	target := filepath.Join(outsideDir, "secret.enc")
	if err := os.WriteFile(target, []byte("not-used"), 0o600); err != nil {
		t.Fatalf("WriteFile(): %v", err)
	}

	if _, err := (handler{}).ReadEncryptedFile(target); err == nil {
		t.Fatal("ReadEncryptedFile() succeeded for file outside allowed roots")
	}
}
