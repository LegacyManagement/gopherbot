package bot

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	privsepChildRoleEnv      = "GOPHER_PRIVSEP_CHILD_ROLE"
	privsepSelfCheckCommand  = "privsep-self-check"
	privsepRoleNone          = privsepChildRole("")
	privsepRolePrivileged    = privsepChildRole("privileged")
	privsepRoleUnprivileged  = privsepChildRole("unprivileged")
	privsepSelfCheckExitFail = 2
)

type privsepChildRole string

type privsepIdentityReport struct {
	UID    int   `json:"uid"`
	EUID   int   `json:"euid"`
	GID    int   `json:"gid"`
	EGID   int   `json:"egid"`
	Groups []int `json:"groups"`
}

type setuidExecutableTarget struct {
	path string
	info os.FileInfo
}

var (
	currentExecutablePath  = os.Executable
	setPrivsepEffectiveUID = switchPrivsepEffectiveUID
	statExecutableTarget   = os.Stat
)

func (r privsepChildRole) valid() bool {
	switch r {
	case privsepRoleNone, privsepRolePrivileged, privsepRoleUnprivileged:
		return true
	default:
		return false
	}
}

func privsepRoleFromString(raw string) (privsepChildRole, error) {
	role := privsepChildRole(strings.TrimSpace(raw))
	if !role.valid() {
		return privsepRoleNone, fmt.Errorf("invalid privsep child role %q", raw)
	}
	return role, nil
}

func privsepRoleEnv(role privsepChildRole) []string {
	if role == privsepRoleNone {
		return nil
	}
	return []string{privsepChildRoleEnv + "=" + string(role)}
}

func privsepRoleForExecution(privileged bool) privsepChildRole {
	if !privSep {
		return privsepRoleNone
	}
	if privileged {
		return privsepRolePrivileged
	}
	return privsepRoleUnprivileged
}

func appendPrivsepRoleEnv(extra []string, role privsepChildRole) []string {
	if role == privsepRoleNone {
		return extra
	}
	return append(extra, privsepRoleEnv(role)...)
}

func commitPrivsepChildFromEnv(required bool) int {
	raw := strings.TrimSpace(os.Getenv(privsepChildRoleEnv))
	if raw == "" {
		if required && privSep {
			fmt.Fprintf(os.Stderr, "Missing %s for privsep child\n", privsepChildRoleEnv)
			return privsepSelfCheckExitFail
		}
		return 0
	}
	role, err := privsepRoleFromString(raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return privsepSelfCheckExitFail
	}
	if role == privsepRoleNone {
		if required && privSep {
			fmt.Fprintf(os.Stderr, "Empty privsep role for required child\n")
			return privsepSelfCheckExitFail
		}
		return 0
	}
	if !privSep {
		fmt.Fprintf(os.Stderr, "Privsep role %q requested but privilege separation is not active\n", role)
		return privsepSelfCheckExitFail
	}
	if err := commitPrivsepChildRole(role); err != nil {
		fmt.Fprintf(os.Stderr, "Committing privsep child role %q: %v\n", role, err)
		return privsepSelfCheckExitFail
	}
	return 0
}

func runPrivsepSelfCheck() int {
	report, err := currentPrivsepIdentityReport()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Collecting privsep identity report: %v\n", err)
		return privsepSelfCheckExitFail
	}
	if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
		fmt.Fprintf(os.Stderr, "Encoding privsep identity report: %v\n", err)
		return privsepSelfCheckExitFail
	}
	return 0
}

func privsepInternalCommandActive() bool {
	if len(os.Args) < 2 {
		return false
	}
	switch os.Args[1] {
	case pipelineChildExecCommand, pipelineChildRPCCommand, privsepSelfCheckCommand:
		return true
	default:
		return false
	}
}

func logPrivsepInitDiagnostic(format string, args ...interface{}) {
	logger := botStdOutLogger
	if privsepInternalCommandActive() {
		logger = botStdErrLogger
	}
	logger.Printf(format, args...)
}

func setuidExecutablePath() (string, error) {
	execPath, err := currentExecutablePath()
	if err != nil {
		return "", err
	}
	return resolveExecutableTarget(execPath)
}

func resolveExecutableTarget(execPath string) (string, error) {
	resolved, err := filepath.EvalSymlinks(execPath)
	if err != nil {
		return "", err
	}
	return resolved, nil
}

func setuidExecutableTargetForTamperCheck(resolveUID, restoreUID int) (target setuidExecutableTarget, err error) {
	err = withPrivsepEffectiveUID(resolveUID, restoreUID, func() error {
		execPath, err := setuidExecutablePath()
		if err != nil {
			return err
		}
		info, err := statExecutableTarget(execPath)
		if err != nil {
			return err
		}
		target = setuidExecutableTarget{
			path: execPath,
			info: info,
		}
		return nil
	})
	return target, err
}

func setuidExecutableTargetForCurrentPrivsep() (setuidExecutableTarget, error) {
	return setuidExecutableTargetForTamperCheck(privUID, unprivUID)
}

func withPrivsepEffectiveUID(resolveUID, restoreUID int, fn func() error) (err error) {
	if resolveUID == restoreUID {
		return fn()
	}
	if err := setPrivsepEffectiveUID(resolveUID); err != nil {
		return fmt.Errorf("switch effective uid to invoking uid %d: %w", resolveUID, err)
	}
	defer func() {
		if restoreErr := setPrivsepEffectiveUID(restoreUID); restoreErr != nil {
			if err != nil {
				err = fmt.Errorf("%w; restoring effective uid to setuid uid %d: %v", err, restoreUID, restoreErr)
				return
			}
			err = fmt.Errorf("restore effective uid to setuid uid %d: %w", restoreUID, restoreErr)
		}
	}()
	return fn()
}

func verifyUnprivilegedExecutableReachability(userName, execPath string) error {
	if _, err := statExecutableTarget(execPath); err != nil {
		return fmt.Errorf("user '%s' unable to traverse directories to '%s' - make sure %s has access to the installation before running setuid %s", userName, execPath, userName, userName)
	}
	return nil
}

func validatePrivsepIdentityReport(report privsepIdentityReport) error {
	if report.UID != unprivUID || report.EUID != unprivUID {
		return fmt.Errorf("privsep self-check unprivileged child UID mismatch: uid/euid %d/%d, want %d/%d", report.UID, report.EUID, unprivUID, unprivUID)
	}
	if report.GID != privGID || report.EGID != privGID {
		return fmt.Errorf("privsep self-check child GID changed: gid/egid %d/%d, want inherited robot gid %d/%d", report.GID, report.EGID, privGID, privGID)
	}
	return nil
}

func validatePrivsepStartupPolicy() error {
	if !privSep {
		return nil
	}
	report, err := runPrivsepStartupSelfCheck()
	if err != nil {
		return err
	}
	return validatePrivsepIdentityReport(report)
}

func runPrivsepStartupSelfCheck() (privsepIdentityReport, error) {
	var report privsepIdentityReport
	cmd := exec.Command(execPath(), privsepSelfCheckCommand)
	env := appendPrivsepRoleEnv(nil, privsepRoleUnprivileged)
	cmd.Env = sanitizedChildEnvironment(env...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		stderrText := strings.TrimSpace(stderr.String())
		if stderrText != "" {
			return report, fmt.Errorf("privsep self-check failed: %v: %s", err, truncatePrivsepOutput(stderrText))
		}
		return report, fmt.Errorf("privsep self-check failed: %v", err)
	}
	if err := decodePrivsepSelfCheckReport(stdout.Bytes(), &report); err != nil {
		stderrText := strings.TrimSpace(stderr.String())
		if stderrText != "" {
			return report, fmt.Errorf("%v; stderr: %s", err, truncatePrivsepOutput(stderrText))
		}
		return report, err
	}
	return report, nil
}

func decodePrivsepSelfCheckReport(out []byte, report *privsepIdentityReport) error {
	if err := json.Unmarshal(out, report); err != nil {
		return fmt.Errorf("decoding privsep self-check report: %v; stdout: %s", err, summarizePrivsepOutput(out))
	}
	return nil
}

func summarizePrivsepOutput(out []byte) string {
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return "<empty>"
	}
	return truncatePrivsepOutput(trimmed)
}

func truncatePrivsepOutput(s string) string {
	const max = 240
	s = strings.ReplaceAll(s, "\n", `\n`)
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}
