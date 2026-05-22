package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseIntegrationSuiteListJSON(t *testing.T) {
	got, err := parseIntegrationSuiteList(`[{"name":"TestLuaFull","config_dir":"test/luafull","metadata":{"subsystems":["extension-api"],"runtimes":["lua"],"tier":"full"}}]`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0]["name"] != "TestLuaFull" {
		t.Fatalf("name = %#v", got[0]["name"])
	}
	metadata, ok := got[0]["metadata"].(map[string]interface{})
	if !ok {
		t.Fatalf("metadata = %#v", got[0]["metadata"])
	}
	if metadata["tier"] != "full" {
		t.Fatalf("tier = %#v", metadata["tier"])
	}
}

func TestParseIntegrationSuiteListTSV(t *testing.T) {
	got, err := parseIntegrationSuiteList("TestBotName\ttest/membrain\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0]["config_dir"] != "test/membrain" {
		t.Fatalf("config_dir = %#v", got[0]["config_dir"])
	}
}

func TestReadIntegrationResultSummariesKeepsFailureDetails(t *testing.T) {
	resultPath := filepath.Join(t.TempDir(), "result.json")
	data := `{
		"suite": "TestMessageMatch",
		"status": "failed",
		"failures": [{
			"suite": "TestMessageMatch",
			"case": "literal-prefix",
			"step": "reply",
			"error": "reply mismatch",
			"sent": "bender match me",
			"expected": "expected reply",
			"seen": "actual reply"
		}]
	}`
	if err := os.WriteFile(resultPath, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	results, warnings := readIntegrationResultSummaries([]string{resultPath}, false, 20, 1024)
	if len(warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", warnings)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	failures, ok := results[0]["failures"].([]integrationFailure)
	if !ok {
		t.Fatalf("failures = %#v", results[0]["failures"])
	}
	if len(failures) != 1 {
		t.Fatalf("len(failures) = %d, want 1", len(failures))
	}
	if failures[0].Sent != "bender match me" || failures[0].Expected != "expected reply" || failures[0].Seen != "actual reply" {
		t.Fatalf("failure details = %#v", failures[0])
	}
}

func TestReadIntegrationFailureSummary(t *testing.T) {
	root := t.TempDir()
	summaryPath := filepath.Join(root, integrationFailureSummaryFile)
	if err := os.WriteFile(summaryPath, []byte("Suite: TestMessageMatch\nError: reply mismatch\n"), 0644); err != nil {
		t.Fatal(err)
	}

	summary, err := readIntegrationFailureSummary(root, 10, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if summary == nil {
		t.Fatal("summary = nil, want populated summary")
	}
	tail, ok := summary["tail"].(map[string]interface{})
	if !ok {
		t.Fatalf("tail = %#v", summary["tail"])
	}
	text, _ := tail["text"].(string)
	if !strings.Contains(text, "reply mismatch") {
		t.Fatalf("summary tail text = %q", text)
	}
}

func TestDiscoverIntegrationFailureSummaryFromResultPath(t *testing.T) {
	root := t.TempDir()
	suiteDir := filepath.Join(root, "TestMessageMatch")
	if err := os.MkdirAll(suiteDir, 0755); err != nil {
		t.Fatal(err)
	}
	resultPath := filepath.Join(suiteDir, "result.json")
	if err := os.WriteFile(resultPath, []byte(`{"suite":"TestMessageMatch"}`), 0644); err != nil {
		t.Fatal(err)
	}
	summaryPath := filepath.Join(root, integrationFailureSummaryFile)
	if err := os.WriteFile(summaryPath, []byte("Suite: TestMessageMatch\n"), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := discoverIntegrationFailureSummaryFiles(resultPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != summaryPath {
		t.Fatalf("summaries = %#v, want [%q]", got, summaryPath)
	}
}

func TestSummarizeIntegrationResults(t *testing.T) {
	results := []map[string]interface{}{
		{
			"suite":       "TestBotName",
			"passed":      false,
			"result_path": "/tmp/TestBotName/result.json",
			"failures": []integrationFailure{
				{
					Suite:    "TestBotName",
					Case:     "case-014",
					Step:     "reply",
					Error:    "message regex mismatch",
					Sent:     "bender echo",
					Expected: "expected reply",
					Seen:     "actual reply",
				},
			},
		},
		{
			"suite":  "TestMessageMatch",
			"passed": true,
		},
	}

	summary := summarizeIntegrationResults(results)
	if summary["suite_count"] != 2 {
		t.Fatalf("suite_count = %#v, want 2", summary["suite_count"])
	}
	if summary["passed_count"] != 1 {
		t.Fatalf("passed_count = %#v, want 1", summary["passed_count"])
	}
	if summary["failed_count"] != 1 {
		t.Fatalf("failed_count = %#v, want 1", summary["failed_count"])
	}
	failedSuites, ok := summary["failed_suites"].([]map[string]interface{})
	if !ok || len(failedSuites) != 1 || failedSuites[0]["suite"] != "TestBotName" {
		t.Fatalf("failed_suites = %#v", summary["failed_suites"])
	}
	failedTests, ok := summary["failed_tests"].([]map[string]interface{})
	if !ok || len(failedTests) != 1 {
		t.Fatalf("failed_tests = %#v", summary["failed_tests"])
	}
	if failedTests[0]["sent"] != "bender echo" || failedTests[0]["expected"] != "expected reply" || failedTests[0]["seen"] != "actual reply" {
		t.Fatalf("failed test details = %#v", failedTests[0])
	}
	report, _ := failedTests[0]["report"].(string)
	for _, want := range []string{
		"TestBotName / case-014 / reply",
		"Input:\n  bender echo",
		"Expected:\n  expected reply",
		"Seen:\n  actual reply",
		"Error:\n  message regex mismatch",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("failure report missing %q in:\n%s", want, report)
		}
	}
}
