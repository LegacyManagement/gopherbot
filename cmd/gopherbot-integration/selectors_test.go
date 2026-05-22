//go:build test

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveSuiteSelectorsBySubsystem(t *testing.T) {
	selected, err := resolveSuiteSelectors([]string{"subsystem:pipeline"})
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) == 0 {
		t.Fatal("subsystem:pipeline selected no suites")
	}
	for _, suite := range selected {
		if !containsAny(suite.Metadata.Subsystems, []string{"pipeline"}) {
			t.Fatalf("%s missing pipeline subsystem: %#v", suite.Name, suite.Metadata)
		}
	}
}

func TestResolveSuiteSelectorsByRuntime(t *testing.T) {
	selected, err := resolveSuiteSelectors([]string{"runtime:lua"})
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) == 0 {
		t.Fatal("runtime:lua selected no suites")
	}
	for _, suite := range selected {
		if !containsAny(suite.Metadata.Runtimes, []string{"lua"}) {
			t.Fatalf("%s missing lua runtime: %#v", suite.Name, suite.Metadata)
		}
	}
}

func TestFormatSuiteReportLine(t *testing.T) {
	tests := []struct {
		name  string
		entry suiteReportEntry
		want  string
	}{
		{
			name:  "pass",
			entry: suiteReportEntry{Suite: "TestBotName"},
			want:  "TestBotName: PASS",
		},
		{
			name:  "failures",
			entry: suiteReportEntry{Suite: "TestBotName", FailureCount: 2},
			want:  "TestBotName: FAIL - 2 test(s) failed",
		},
		{
			name:  "run error",
			entry: suiteReportEntry{Suite: "TestBotName", RunError: "exit status 1"},
			want:  "TestBotName: FAIL - exit status 1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatSuiteReportLine(tt.entry); got != tt.want {
				t.Fatalf("formatSuiteReportLine() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPrintSuiteReport(t *testing.T) {
	var out bytes.Buffer
	printSuiteReport(&out, []suiteReportEntry{
		{Suite: "TestBotName"},
		{Suite: "TestMessageMatch", FailureCount: 1},
	})
	got := out.String()
	for _, want := range []string{
		"Summary report:\n",
		"TestBotName: PASS\n",
		"TestMessageMatch: FAIL - 1 test(s) failed\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary report missing %q in:\n%s", want, got)
		}
	}
}

func TestWriteSuiteReportFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run", suiteSummaryFile)
	if err := writeSuiteReportFile(path, []suiteReportEntry{
		{Suite: "TestBotName"},
		{Suite: "TestMessageMatch", FailureCount: 2},
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{
		"Summary report:\n",
		"TestBotName: PASS\n",
		"TestMessageMatch: FAIL - 2 test(s) failed\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary file missing %q in:\n%s", want, got)
		}
	}
}
