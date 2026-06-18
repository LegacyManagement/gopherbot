package gsh

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lnxjedi/gopherbot/robot"
)

func writeTempScript(t *testing.T, dir, name, contents string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		t.Fatalf("writing temp script: %v", err)
	}
	return path
}

func TestRunScriptUtilityBuiltins(t *testing.T) {
	tmp := t.TempDir()
	script := writeTempScript(t, tmp, "utilities.gsh", `#!/bin/sh
tmpdir=$(mktemp -d "$GOPHER_WORKSPACE/shfull.XXXXXX") || exit 10
mkdir -p "$tmpdir/a" || exit 11
printf 'beta\nalpha\nbeta\n' > "$tmpdir/a/input.txt"
cp "$tmpdir/a/input.txt" "$tmpdir/a/copy.txt" || exit 12
mv "$tmpdir/a/copy.txt" "$tmpdir/a/moved.txt" || exit 13
touch "$tmpdir/a/marker.txt" || exit 14
printf 'ship' | base64 > "$tmpdir/a/encoded.txt"
decoded=$(base64 -d "$tmpdir/a/encoded.txt") || exit 15
printf '{"phase":"go"}\n' > "$tmpdir/a/data.json"
jq_phase=$(jq -r '.phase' "$tmpdir/a/data.json") || exit 18
gzip "$tmpdir/a/moved.txt" || exit 16
gunzip "$tmpdir/a/moved.txt.gz" || exit 17
head_line=$(head -n 1 "$tmpdir/a/moved.txt")
tail_line=$(tail -n 1 "$tmpdir/a/moved.txt")
line_info=$(wc -l "$tmpdir/a/moved.txt")
set -- $line_info
line_count=$1
uniq_lines=$(cat "$tmpdir/a/moved.txt" | sort | uniq)
printf 'head=%s tail=%s lines=%s decode=%s jq=%s uniq=%s\n' "$head_line" "$tail_line" "$line_count" "$decoded" "$jq_phase" "$(printf '%s' "$uniq_lines" | tr '\n' ',')"
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	ret, err := runScript(
		script,
		"utilities-test",
		tmp,
		[]string{
			"GOPHER_WORKSPACE=" + tmp,
			"GOPHER_INSTALLDIR=" + tmp,
		},
		nil,
		nil,
		nil,
		&stdout,
		&stderr,
	)
	if err != nil {
		t.Fatalf("runScript() error = %v; stderr=%q", err, stderr.String())
	}
	if ret != robot.Normal {
		t.Fatalf("runScript() ret = %v, want %v; stderr=%q", ret, robot.Normal, stderr.String())
	}
	got := strings.TrimSpace(stdout.String())
	want := "head=beta tail=beta lines=3 decode=ship jq=go uniq=alpha,beta"
	if got != want {
		t.Fatalf("utility output = %q, want %q", got, want)
	}
}

func TestRunScriptTailShorthandReadsPipedExternalOutput(t *testing.T) {
	tmp := t.TempDir()
	writeTempScript(t, tmp, "emit-lines", `#!/bin/sh
printf 'one\n'
printf 'two\n'
printf 'three\n'
`)
	script := writeTempScript(t, tmp, "tail-shorthand.gsh", `#!/bin/sh
output=$(emit-lines | tail -1)
printf 'last=%s\n' "$output"
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	ret, err := runScript(
		script,
		"tail-shorthand-test",
		tmp,
		[]string{
			"PATH=" + tmp + string(os.PathListSeparator) + os.Getenv("PATH"),
			"GOPHER_INSTALLDIR=" + tmp,
		},
		nil,
		nil,
		nil,
		&stdout,
		&stderr,
	)
	if err != nil {
		t.Fatalf("runScript() error = %v; stderr=%q", err, stderr.String())
	}
	if ret != robot.Normal {
		t.Fatalf("runScript() ret = %v, want %v; stderr=%q", ret, robot.Normal, stderr.String())
	}
	got := strings.TrimSpace(stdout.String())
	if got != "last=three" {
		t.Fatalf("tail shorthand output = %q, want %q; stderr=%q", got, "last=three", stderr.String())
	}
}

func TestRunScriptUsesWorkDirInsteadOfScriptDir(t *testing.T) {
	home := t.TempDir()
	scriptDir := filepath.Join(home, "jobs")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatalf("creating script dir: %v", err)
	}
	if err := os.Mkdir(filepath.Join(home, "custom"), 0o755); err != nil {
		t.Fatalf("creating custom dir: %v", err)
	}
	outPath := filepath.Join(home, "cwd.txt")
	script := writeTempScript(t, scriptDir, "install-libs.gsh", `#!/bin/sh
pwd > "$OUT_PATH"
cd custom
pwd >> "$OUT_PATH"
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	ret, err := runScript(
		script,
		"install-libs-test",
		home,
		[]string{
			"OUT_PATH=" + outPath,
			"GOPHER_INSTALLDIR=" + home,
		},
		nil,
		nil,
		nil,
		&stdout,
		&stderr,
	)
	if err != nil {
		t.Fatalf("runScript() error = %v; stderr=%q", err, stderr.String())
	}
	if ret != robot.Normal {
		t.Fatalf("runScript() ret = %v, want %v; stderr=%q", ret, robot.Normal, stderr.String())
	}
	gotBytes, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("reading cwd output: %v", err)
	}
	got := strings.Split(strings.TrimSpace(string(gotBytes)), "\n")
	if len(got) != 2 {
		t.Fatalf("cwd output lines = %#v, want two lines", got)
	}
	if got[0] != home {
		t.Fatalf("initial cwd = %q, want %q", got[0], home)
	}
	if got[1] != filepath.Join(home, "custom") {
		t.Fatalf("cwd after cd custom = %q, want %q", got[1], filepath.Join(home, "custom"))
	}
}

func TestParseLogLevelSupportsNumericAndNamedLevels(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  robot.LogLevel
	}{
		{name: "numeric audit", input: "3", want: robot.Audit},
		{name: "named trace", input: "Trace", want: robot.Trace},
		{name: "named debug lower", input: "debug", want: robot.Debug},
		{name: "named info", input: "Info", want: robot.Info},
		{name: "named audit", input: "Audit", want: robot.Audit},
		{name: "named warn", input: "Warn", want: robot.Warn},
		{name: "named warning", input: "Warning", want: robot.Warn},
		{name: "named error", input: "Error", want: robot.Error},
		{name: "named fatal matches external shell behavior", input: "Fatal", want: robot.Error},
		{name: "unknown matches external shell behavior", input: "NoSuchLevel", want: robot.Error},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseLogLevel(tt.input); got != tt.want {
				t.Fatalf("parseLogLevel(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
