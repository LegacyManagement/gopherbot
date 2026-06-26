package bot

import (
	"strings"
	"testing"

	"github.com/lnxjedi/gopherbot/robot"
)

func envValue(env []string, key string) (string, bool) {
	prefix := key + "="
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			return strings.TrimPrefix(item, prefix), true
		}
	}
	return "", false
}

func TestGetEnvironmentPreservesParentHomeAndPath(t *testing.T) {
	oldHomePath := homePath
	homePath = "/robot/home"
	t.Cleanup(func() {
		homePath = oldHomePath
	})
	t.Setenv("HOME", "/users/dev")
	t.Setenv("PATH", "/opt/dev/bin:/usr/bin")

	w := &worker{
		User:     "alice",
		Channel:  "general",
		Incoming: &robot.ConnectorMessage{},
		cfg: &configuration{
			brainProvider: "mem",
			workSpace:     "/robot/workspace",
		},
		pipeContext: &pipeContext{
			taskName: "envtest",
			ptype:    plugCommand,
		},
	}
	task := &Task{name: "envtest"}

	env, _ := w.getEnvironment(task)
	if got := env["HOME"]; got != "/users/dev" {
		t.Fatalf("HOME = %q, want inherited parent HOME", got)
	}
	if got := env["PATH"]; got != "/opt/dev/bin:/usr/bin" {
		t.Fatalf("PATH = %q, want inherited parent PATH", got)
	}
	if got := env["GOPHER_HOME"]; got != "/robot/home" {
		t.Fatalf("GOPHER_HOME = %q, want robot home", got)
	}
	if got := env["GEM_HOME"]; got != "/robot/home/.bot-gems" {
		t.Fatalf("GEM_HOME = %q, want robot-managed gem cache", got)
	}
	if got := env["GEM_PATH"]; got != "/robot/home/.bot-gems" {
		t.Fatalf("GEM_PATH = %q, want robot-managed gem cache", got)
	}
	if got := env["PYTHONUSERBASE"]; got != "/robot/home/.bot-python" {
		t.Fatalf("PYTHONUSERBASE = %q, want robot-managed python cache", got)
	}
}

func TestBuildConfigureEnvPreservesParentHomeAndPath(t *testing.T) {
	oldHomePath := homePath
	oldInstallPath := installPath
	oldConfigFull := configFull
	homePath = "/robot/home"
	installPath = "/robot/install"
	configFull = "/robot/config"
	t.Cleanup(func() {
		homePath = oldHomePath
		installPath = oldInstallPath
		configFull = oldConfigFull
	})
	t.Setenv("HOME", "/users/dev")
	t.Setenv("PATH", "/opt/dev/bin:/usr/bin")

	env := buildConfigureEnv()
	if got, ok := envValue(env, "HOME"); !ok || got != "/users/dev" {
		t.Fatalf("HOME = %q, present=%v; want inherited parent HOME", got, ok)
	}
	if got, ok := envValue(env, "PATH"); !ok || got != "/opt/dev/bin:/usr/bin" {
		t.Fatalf("PATH = %q, present=%v; want inherited parent PATH", got, ok)
	}
	if got, ok := envValue(env, "GOPHER_HOME"); !ok || got != "/robot/home" {
		t.Fatalf("GOPHER_HOME = %q, present=%v; want robot home", got, ok)
	}
	if got, ok := envValue(env, "GEM_HOME"); !ok || got != "/robot/home/.bot-gems" {
		t.Fatalf("GEM_HOME = %q, present=%v; want robot-managed gem cache", got, ok)
	}
	if got, ok := envValue(env, "GEM_PATH"); !ok || got != "/robot/home/.bot-gems" {
		t.Fatalf("GEM_PATH = %q, present=%v; want robot-managed gem cache", got, ok)
	}
	if got, ok := envValue(env, "PYTHONUSERBASE"); !ok || got != "/robot/home/.bot-python" {
		t.Fatalf("PYTHONUSERBASE = %q, present=%v; want robot-managed python cache", got, ok)
	}
}
