package bot

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
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
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr := strings.TrimSpace(string(exitErr.Stderr))
			if stderr != "" {
				return report, fmt.Errorf("privsep self-check failed: %v: %s", err, stderr)
			}
		}
		return report, fmt.Errorf("privsep self-check failed: %v", err)
	}
	if err := json.Unmarshal(out, &report); err != nil {
		return report, fmt.Errorf("decoding privsep self-check report: %v", err)
	}
	return report, nil
}
