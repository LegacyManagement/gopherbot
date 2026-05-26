package bot

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseQueueBodyNoArgs(t *testing.T) {
	parsed, err := parseQueueBody([]byte("1104df4c-feeb-43ab-8c85-83663288cea9:17642656976077"))
	if err != nil {
		t.Fatalf("parseQueueBody returned error: %v", err)
	}
	if parsed.jobUUID != "1104df4c-feeb-43ab-8c85-83663288cea9" {
		t.Fatalf("id = %q", parsed.jobUUID)
	}
	if parsed.timestamp != "17642656976077" {
		t.Fatalf("timestamp = %q", parsed.timestamp)
	}
	if len(parsed.args) != 0 {
		t.Fatalf("args = %#v, want none", parsed.args)
	}
}

func TestParseQueueBodyShellEscapedArgs(t *testing.T) {
	parsed, err := parseQueueBody([]byte("1104df4c-feeb-43ab-8c85-83663288cea9:17642656976077 alpha two\\ words 'three four'"))
	if err != nil {
		t.Fatalf("parseQueueBody returned error: %v", err)
	}
	if parsed.jobUUID != "1104df4c-feeb-43ab-8c85-83663288cea9" {
		t.Fatalf("id = %q", parsed.jobUUID)
	}
	if parsed.timestamp != "17642656976077" {
		t.Fatalf("timestamp = %q", parsed.timestamp)
	}
	want := []string{"alpha", "two words", "three four"}
	if !reflect.DeepEqual(parsed.args, want) {
		t.Fatalf("args = %#v, want %#v", parsed.args, want)
	}
}

func TestParseQueueBodyRejectsMalformedBody(t *testing.T) {
	tests := []struct {
		name    string
		body    []byte
		errPart string
	}{
		{name: "too short", body: []byte("short"), errPart: "too short"},
		{name: "invalid uuid", body: []byte("xxxxxxxx-feeb-43ab-8c85-83663288cea9:17642656976077 arg"), errPart: "invalid queue UUID prefix"},
		{name: "short timestamp", body: []byte("1104df4c-feeb-43ab-8c85-83663288cea9:12345678901 arg"), errPart: "timestamp must be 12-15 digits"},
		{name: "long timestamp", body: []byte("1104df4c-feeb-43ab-8c85-83663288cea9:1234567890123456 arg"), errPart: "timestamp must be 12-15 digits"},
		{name: "non-numeric timestamp", body: []byte("1104df4c-feeb-43ab-8c85-83663288cea9:1764265697607x arg"), errPart: "timestamp is not followed by a space"},
		{name: "unterminated shell arg", body: []byte("1104df4c-feeb-43ab-8c85-83663288cea9:17642656976077 'unterminated"), errPart: "parsing shell-escaped queue arguments"},
	}
	for _, tc := range tests {
		_, err := parseQueueBody(tc.body)
		if err == nil {
			t.Fatalf("%s: parseQueueBody(%q) returned nil error", tc.name, string(tc.body))
		}
		if !strings.Contains(err.Error(), tc.errPart) {
			t.Fatalf("%s: parseQueueBody(%q) error = %q, want substring %q", tc.name, string(tc.body), err, tc.errPart)
		}
	}
}

func TestQueueDedupeRecordsUUIDTimestampPairs(t *testing.T) {
	queueDedupe.Lock()
	queueDedupe.seen = map[string]time.Time{}
	queueDedupe.Unlock()

	now := time.Unix(100, 0)
	if recordQueueDedupe("1104df4c-feeb-43ab-8c85-83663288cea9", "17642656976077", now) {
		t.Fatal("first UUID/timestamp pair was reported as duplicate")
	}
	if !recordQueueDedupe("1104df4c-feeb-43ab-8c85-83663288cea9", "17642656976077", now.Add(time.Second)) {
		t.Fatal("repeated UUID/timestamp pair was not reported as duplicate")
	}
	if recordQueueDedupe("1104df4c-feeb-43ab-8c85-83663288cea9", "17642656976078", now.Add(2*time.Second)) {
		t.Fatal("different timestamp was reported as duplicate")
	}
}

func TestQueueDedupeExpiresAfterRetention(t *testing.T) {
	queueDedupe.Lock()
	queueDedupe.seen = map[string]time.Time{}
	queueDedupe.Unlock()

	now := time.Unix(200, 0)
	if recordQueueDedupe("1104df4c-feeb-43ab-8c85-83663288cea9", "17642656976077", now) {
		t.Fatal("first UUID/timestamp pair was reported as duplicate")
	}
	if recordQueueDedupe("1104df4c-feeb-43ab-8c85-83663288cea9", "17642656976077", now.Add(queueDedupeRetention)) {
		t.Fatal("expired UUID/timestamp pair was reported as duplicate")
	}
}
