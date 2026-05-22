package bot

import (
	"bytes"
	"errors"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestValidatePrivsepIdentityReportAcceptsUIDOnlyRole(t *testing.T) {
	oldUnprivUID := unprivUID
	t.Cleanup(func() {
		unprivUID = oldUnprivUID
	})
	unprivUID = 65534

	report := privsepIdentityReport{UID: 65534, EUID: 65534}
	if err := validatePrivsepIdentityReport(report); err != nil {
		t.Fatalf("validatePrivsepIdentityReport() error = %v", err)
	}
}

func TestValidatePrivsepIdentityReportRejectsUIDMismatch(t *testing.T) {
	oldUnprivUID := unprivUID
	t.Cleanup(func() {
		unprivUID = oldUnprivUID
	})
	unprivUID = 65534

	report := privsepIdentityReport{UID: 1000, EUID: 65534}
	if err := validatePrivsepIdentityReport(report); err == nil {
		t.Fatal("validatePrivsepIdentityReport() error = nil, want UID mismatch")
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

func TestDecodePrivsepSelfCheckReportReportsContaminatedStdout(t *testing.T) {
	var report privsepIdentityReport
	err := decodePrivsepSelfCheckReport([]byte("PRIVSEP - noisy diagnostic\n{\"uid\":65534}\n"), &report)
	if err == nil {
		t.Fatal("decodePrivsepSelfCheckReport() error = nil, want contaminated stdout error")
	}
	for _, want := range []string{
		"decoding privsep self-check report",
		"invalid character 'P'",
		"stdout: PRIVSEP - noisy diagnostic",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("decodePrivsepSelfCheckReport() error = %q, want substring %q", err.Error(), want)
		}
	}
}

func TestLogPrivsepInitDiagnosticUsesStderrForInternalChildCommand(t *testing.T) {
	oldArgs := os.Args
	oldStdout := botStdOutLogger
	oldStderr := botStdErrLogger
	t.Cleanup(func() {
		os.Args = oldArgs
		botStdOutLogger = oldStdout
		botStdErrLogger = oldStderr
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	botStdOutLogger = log.New(&stdout, "", 0)
	botStdErrLogger = log.New(&stderr, "", 0)
	os.Args = []string{"gopherbot", privsepSelfCheckCommand}

	logPrivsepInitDiagnostic("PRIVSEP - %s", "diagnostic")

	if stdout.Len() != 0 {
		t.Fatalf("stdout log = %q, want empty", stdout.String())
	}
	if got := stderr.String(); !strings.Contains(got, "PRIVSEP - diagnostic") {
		t.Fatalf("stderr log = %q, want privsep diagnostic", got)
	}
}

func TestResolveExecutableTargetAllowsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "gopherbot-real")
	link := filepath.Join(dir, "gopherbot")
	if err := os.WriteFile(target, []byte("binary"), 0700); err != nil {
		t.Fatalf("WriteFile target: %v", err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	got, err := resolveExecutableTarget(link)
	if err != nil {
		t.Fatalf("resolveExecutableTarget() error = %v", err)
	}
	want, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatalf("EvalSymlinks target: %v", err)
	}
	if got != want {
		t.Fatalf("resolveExecutableTarget() = %q, want %q", got, want)
	}
}

func TestSetuidExecutableTargetSwitchesToInvokerForSymlinkInspection(t *testing.T) {
	dir := t.TempDir()
	realExe := filepath.Join(dir, "gopherbot-real")
	linkExe := filepath.Join(dir, "gopherbot")
	if err := os.WriteFile(realExe, []byte("binary"), 04755); err != nil {
		t.Fatalf("WriteFile realExe: %v", err)
	}
	if err := os.Symlink(realExe, linkExe); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	oldExecutablePath := currentExecutablePath
	oldSetEffectiveUID := setPrivsepEffectiveUID
	oldStatExecutableTarget := statExecutableTarget
	t.Cleanup(func() {
		currentExecutablePath = oldExecutablePath
		setPrivsepEffectiveUID = oldSetEffectiveUID
		statExecutableTarget = oldStatExecutableTarget
	})

	var switched []int
	currentExecutablePath = func() (string, error) {
		return linkExe, nil
	}
	setPrivsepEffectiveUID = func(uid int) error {
		switched = append(switched, uid)
		return nil
	}

	target, err := setuidExecutableTargetForTamperCheck(1000, 65534)
	if err != nil {
		t.Fatalf("setuidExecutableTargetForTamperCheck() error = %v", err)
	}
	wantPath, err := filepath.EvalSymlinks(realExe)
	if err != nil {
		t.Fatalf("EvalSymlinks realExe: %v", err)
	}
	if target.path != wantPath {
		t.Fatalf("target path = %q, want %q", target.path, wantPath)
	}
	if target.info == nil || !target.info.Mode().IsRegular() {
		t.Fatalf("target info = %#v, want regular file info", target.info)
	}
	if want := []int{1000, 65534}; !reflect.DeepEqual(switched, want) {
		t.Fatalf("effective UID switches = %v, want %v", switched, want)
	}
}

func TestSetuidExecutableTargetRestoresSetuidUIDAfterInspectionFailure(t *testing.T) {
	oldExecutablePath := currentExecutablePath
	oldSetEffectiveUID := setPrivsepEffectiveUID
	oldStatExecutableTarget := statExecutableTarget
	t.Cleanup(func() {
		currentExecutablePath = oldExecutablePath
		setPrivsepEffectiveUID = oldSetEffectiveUID
		statExecutableTarget = oldStatExecutableTarget
	})

	var switched []int
	currentExecutablePath = func() (string, error) {
		return "", errors.New("no executable")
	}
	setPrivsepEffectiveUID = func(uid int) error {
		switched = append(switched, uid)
		return nil
	}

	_, err := setuidExecutableTargetForTamperCheck(1000, 65534)
	if err == nil {
		t.Fatal("setuidExecutableTargetForTamperCheck() error = nil, want failure")
	}
	if want := []int{1000, 65534}; !reflect.DeepEqual(switched, want) {
		t.Fatalf("effective UID switches = %v, want %v", switched, want)
	}
}

func TestVerifyUnprivilegedExecutableReachabilityReportsTraversalFailure(t *testing.T) {
	oldStatExecutableTarget := statExecutableTarget
	t.Cleanup(func() {
		statExecutableTarget = oldStatExecutableTarget
	})

	statExecutableTarget = func(name string) (os.FileInfo, error) {
		return nil, os.ErrPermission
	}

	const execPath = "/home/david/git/gopherbot/gopherbot"
	err := verifyUnprivilegedExecutableReachability("nobody", execPath)
	if err == nil {
		t.Fatal("verifyUnprivilegedExecutableReachability() error = nil, want traversal failure")
	}
	want := "user 'nobody' unable to traverse directories to '/home/david/git/gopherbot/gopherbot' - make sure nobody has access to the installation before running setuid nobody"
	if err.Error() != want {
		t.Fatalf("verifyUnprivilegedExecutableReachability() error = %q, want %q", err.Error(), want)
	}
}
