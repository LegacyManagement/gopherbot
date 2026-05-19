package bot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidatePrivsepIdentityReportAcceptsUIDOnlyRole(t *testing.T) {
	oldUnprivUID := unprivUID
	oldPrivGID := privGID
	t.Cleanup(func() {
		unprivUID = oldUnprivUID
		privGID = oldPrivGID
	})
	unprivUID = 65534
	privGID = 1000

	report := privsepIdentityReport{
		UID:    65534,
		EUID:   65534,
		GID:    1000,
		EGID:   1000,
		Groups: []int{1000, 2000},
	}
	if err := validatePrivsepIdentityReport(report); err != nil {
		t.Fatalf("validatePrivsepIdentityReport() error = %v", err)
	}
}

func TestValidatePrivsepIdentityReportRejectsUIDMismatch(t *testing.T) {
	oldUnprivUID := unprivUID
	oldPrivGID := privGID
	t.Cleanup(func() {
		unprivUID = oldUnprivUID
		privGID = oldPrivGID
	})
	unprivUID = 65534
	privGID = 1000

	report := privsepIdentityReport{UID: 1000, EUID: 65534, GID: 1000, EGID: 1000}
	if err := validatePrivsepIdentityReport(report); err == nil {
		t.Fatal("validatePrivsepIdentityReport() error = nil, want UID mismatch")
	}
}

func TestValidatePrivsepIdentityReportRejectsGIDChange(t *testing.T) {
	oldUnprivUID := unprivUID
	oldPrivGID := privGID
	t.Cleanup(func() {
		unprivUID = oldUnprivUID
		privGID = oldPrivGID
	})
	unprivUID = 65534
	privGID = 1000

	report := privsepIdentityReport{UID: 65534, EUID: 65534, GID: 65534, EGID: 65534}
	if err := validatePrivsepIdentityReport(report); err == nil {
		t.Fatal("validatePrivsepIdentityReport() error = nil, want inherited GID mismatch")
	}
}

func TestLoadConfigRejectsRemovedPrivsepGroupKeys(t *testing.T) {
	oldInstallPath := installPath
	oldConfigPath := configPath
	t.Cleanup(func() {
		installPath = oldInstallPath
		configPath = oldConfigPath
	})

	for _, key := range []string{"PrivsepAllowAllSupplementaryGroups", "PrivsepAllowedSupplementaryGroups"} {
		t.Run(key, func(t *testing.T) {
			root := t.TempDir()
			installPath = root
			configPath = filepath.Join(root, "custom")
			if err := os.MkdirAll(filepath.Join(root, "conf"), 0700); err != nil {
				t.Fatalf("MkdirAll conf: %v", err)
			}
			if err := os.WriteFile(filepath.Join(root, "conf", "robot.yaml"), []byte(key+": []\n"), 0600); err != nil {
				t.Fatalf("WriteFile robot.yaml: %v", err)
			}

			err := loadConfig(true)
			if err == nil {
				t.Fatal("loadConfig() error = nil, want invalid configuration key")
			}
			if !strings.Contains(err.Error(), "field "+key+" not found") {
				t.Fatalf("loadConfig() error = %q, want removed key failure for %s", err.Error(), key)
			}
		})
	}
}
