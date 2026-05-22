//go:build !(linux || dragonfly || freebsd || netbsd || openbsd || darwin)

package bot

import "github.com/lnxjedi/gopherbot/robot"

// privSep is always disabled on platforms without privilege separation support.
var privSep bool
var privUID, unprivUID int

func commitPrivsepChildRole(role privsepChildRole) error {
	return nil
}

func currentPrivsepIdentityReport() (privsepIdentityReport, error) {
	return privsepIdentityReport{}, nil
}

func switchPrivsepEffectiveUID(uid int) error {
	return nil
}

func checkprivsep() {
	Log(robot.Info, "PRIVSEP - Privilege separation not available on this platform")
}
