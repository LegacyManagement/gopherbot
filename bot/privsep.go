//go:build linux || dragonfly || freebsd || netbsd || openbsd

package bot

import (
	"fmt"
	"os"
	"os/user"
	"strconv"
	"syscall"

	"github.com/lnxjedi/gopherbot/robot"
	"golang.org/x/sys/unix"
)

// privSep indicates whether privilege separation is active.
// It is set to true only if privilege separation is successfully initialized.
var privSep bool

var privUID, unprivUID int

/* NOTE on privsep and setuid gopherbot:
Gopherbot "flips" the traditional sense of setuid; gopherbot is normally run
by the desired user, and installed setuid to a non-privileged account like
"nobody". This makes it possible to run several instances of gopherbot with
different UIDs on a single host with a single install.

The parent engine swaps its effective UID back to the invoking user while
preserving the setuid nobody saved UID. File-backed extensions then run
in one-shot child processes that permanently commit to either the invoking user
or the unprivileged account before extension code starts.

There are no mid-process privilege transitions in the process-oriented model.
The parent engine runs as the invoking user. File-backed extension children
commit once, before extension code starts, to either the invoking user or the
setuid unprivileged UID. Privsep intentionally does not change GID; host-level
robot privileges should be granted by UID, not by group membership.

*/

func lookupNobodyUID() (int, error) {
	nobody, err := user.Lookup("nobody")
	if err != nil {
		return -1, err
	}
	uid, err := strconv.Atoi(nobody.Uid)
	if err != nil {
		return -1, err
	}
	return uid, nil
}

func panicIfSetuidBinaryTampered(unprivUID int) {
	nobodyUID, err := lookupNobodyUID()
	if err != nil {
		panic("binary could be tampered! unable to resolve nobody uid")
	}
	if unprivUID != nobodyUID {
		return
	}
	target, err := setuidExecutableTargetForCurrentPrivsep()
	if err != nil {
		panic("binary could be tampered! unable to resolve executable path")
	}
	execPath := target.path
	info := target.info
	if err := verifyUnprivilegedExecutableReachability("nobody", execPath); err != nil {
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
	if int(st.Uid) != nobodyUID {
		panic(fmt.Sprintf("binary could be tampered! setuid executable owner mismatch for %s: got uid %d, want nobody uid %d", execPath, st.Uid, nobodyUID))
	}
}

func switchPrivsepEffectiveUID(uid int) error {
	return syscall.Seteuid(uid)
}

func init() {
	uid := unix.Getuid()
	euid := unix.Geteuid()
	if uid != euid {
		privUID = uid
		unprivUID = euid
		panicIfSetuidBinaryTampered(unprivUID)
		unix.Umask(0027)

		// Keep the parent engine on the invoking UID while preserving the
		// setuid nobody saved UID for child process role commits.
		if err := syscall.Setreuid(-1, privUID); err != nil {
			logPrivsepInitDiagnostic("PRIVSEP - error setting reuid in init: %v", err)
			return
		}

		// Check and ensure the current working directory has permissions 0755
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

		mode := info.Mode().Perm()
		if mode != 0755 {
			err = os.Chmod(cwd, 0755)
			if err != nil {
				logPrivsepInitDiagnostic("PRIVSEP - error changing permissions of current working directory '%s' to 0755: %v", cwd, err)
				return
			}
			logPrivsepInitDiagnostic("PRIVSEP - changed permissions of current working directory '%s' from %o to 0755", cwd, mode)
		}

		// Successfully initialized privilege separation
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
		UID:  unix.Getuid(),
		EUID: unix.Geteuid(),
	}, nil
}

// checkprivsep logs the current state of privilege separation.
// It reports whether privilege separation is active and details the UIDs
// associated with the daemon and the current thread.
func checkprivsep() {
	if privSep {
		ruid := unix.Getuid()
		euid := unix.Geteuid()
		tid := unix.Gettid()
		Log(robot.Info, "PRIVSEP - UID-only privilege separation initialized; daemon UID %d, unprivileged UID %d; thread %d r/euid: %d/%d", privUID, unprivUID, tid, ruid, euid)
	} else {
		Log(robot.Info, "PRIVSEP - Privilege separation not in use")
	}
}
