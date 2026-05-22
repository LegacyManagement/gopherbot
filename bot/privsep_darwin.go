//go:build darwin

package bot

import (
	"fmt"
	"os"
	"syscall"

	"github.com/lnxjedi/gopherbot/robot"
)

// privSep indicates whether privilege separation is active.
// It is set to true only if privilege separation is successfully initialized.
var privSep bool

var privUID, unprivUID int

func panicIfDarwinSetuidBinaryTampered(unprivUID int) {
	target, err := setuidExecutableTargetForCurrentPrivsep()
	if err != nil {
		panic("binary could be tampered! unable to resolve executable path")
	}
	execPath := target.path
	info := target.info
	if err := verifyUnprivilegedExecutableReachability(fmt.Sprintf("uid %d", unprivUID), execPath); err != nil {
		panic(err.Error())
	}
	if !info.Mode().IsRegular() {
		panic("binary could be tampered! executable path is not a regular file")
	}
	if info.Mode()&os.ModeSetuid == 0 {
		panic("binary could be tampered! expected setuid bit on executable")
	}
	if info.Mode()&os.ModeSetgid != 0 {
		panic("binary could be tampered! setgid bit is not supported for UID-only privsep")
	}
	if info.Mode().Perm()&0o022 != 0 {
		panic("binary could be tampered! setuid executable is group/world writable")
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		panic("binary could be tampered! unable to verify executable owner")
	}
	if int(st.Uid) != unprivUID {
		panic(fmt.Sprintf("binary could be tampered! setuid executable owner mismatch for %s: got uid %d, want unprivileged uid %d", execPath, st.Uid, unprivUID))
	}
}

func switchPrivsepEffectiveUID(uid int) error {
	return syscall.Seteuid(uid)
}

func init() {
	uid := syscall.Getuid()
	euid := syscall.Geteuid()
	if uid != euid {
		privUID = uid
		unprivUID = euid
		panicIfDarwinSetuidBinaryTampered(unprivUID)
		syscall.Umask(0027)

		// Darwin keeps the saved setuid value when only the effective UID is
		// swapped back to the invoking user. Children re-exec the same setuid
		// binary and permanently commit before extension code starts.
		if err := syscall.Setreuid(-1, privUID); err != nil {
			logPrivsepInitDiagnostic("PRIVSEP - error setting reuid in init: %v", err)
			return
		}

		cwd, err := os.Getwd()
		if err != nil {
			logPrivsepInitDiagnostic("PRIVSEP - error getting current working directory: %v", err)
			return
		}
		info, err := os.Stat(cwd)
		if err != nil {
			logPrivsepInitDiagnostic("PRIVSEP - error stating current working directory '%s': %v", cwd, err)
			return
		}
		if mode := info.Mode().Perm(); mode != 0755 {
			if err := os.Chmod(cwd, 0755); err != nil {
				logPrivsepInitDiagnostic("PRIVSEP - error changing permissions of current working directory '%s' to 0755: %v", cwd, err)
				return
			}
			logPrivsepInitDiagnostic("PRIVSEP - changed permissions of current working directory '%s' from %o to 0755", cwd, mode)
		}

		privSep = true
	}
}

func commitPrivsepChildRole(role privsepChildRole) error {
	switch role {
	case privsepRolePrivileged:
		if err := syscall.Setreuid(privUID, privUID); err != nil {
			return fmt.Errorf("setreuid privileged: %w", err)
		}
	case privsepRoleUnprivileged:
		if err := syscall.Seteuid(unprivUID); err != nil {
			return fmt.Errorf("seteuid unprivileged: %w", err)
		}
		if err := syscall.Setreuid(unprivUID, unprivUID); err != nil {
			return fmt.Errorf("setreuid unprivileged: %w", err)
		}
	default:
		return fmt.Errorf("unsupported role %q", role)
	}
	return nil
}

func currentPrivsepIdentityReport() (privsepIdentityReport, error) {
	return privsepIdentityReport{
		UID:  syscall.Getuid(),
		EUID: syscall.Geteuid(),
	}, nil
}

func checkprivsep() {
	if privSep {
		Log(robot.Info, "PRIVSEP - UID-only privilege separation initialized; daemon UID %d, unprivileged UID %d; r/euid: %d/%d", privUID, unprivUID, syscall.Getuid(), syscall.Geteuid())
	} else {
		Log(robot.Info, "PRIVSEP - Privilege separation not in use")
	}
}
