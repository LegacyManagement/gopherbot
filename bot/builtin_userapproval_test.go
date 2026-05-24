package bot

import (
	"reflect"
	"testing"
)

func TestEffectiveApprovalApprovers(t *testing.T) {
	cfg := userApprovalConfig{
		FallbackApprovers: []string{"david", "alice"},
		PluginApprovers: map[string][]string{
			"wireguard": []string{" Alice ", "bob", "alice", "david", ""},
		},
	}

	got := effectiveApprovalApprovers(cfg, "wireguard", "alice")
	want := []string{"bob", "david"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("effectiveApprovalApprovers plugin list = %#v, want %#v", got, want)
	}

	got = effectiveApprovalApprovers(cfg, "other", "carol")
	want = []string{"david", "alice"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("effectiveApprovalApprovers fallback list = %#v, want %#v", got, want)
	}
}

func TestUserApprovalChoiceMapping(t *testing.T) {
	approvers := []string{"david", "bob", "alice"}
	for _, tc := range []struct {
		choice string
		want   string
		ok     bool
	}{
		{"a", "david", true},
		{"b", "bob", true},
		{"c", "alice", true},
		{"d", "", false},
		{"A", "", false},
		{"aa", "", false},
	} {
		got, ok := userApprovalApproverForChoice(tc.choice, approvers)
		if got != tc.want || ok != tc.ok {
			t.Fatalf("userApprovalApproverForChoice(%q) = %q, %v; want %q, %v", tc.choice, got, ok, tc.want, tc.ok)
		}
	}
}

func TestUserApprovalChoicePrompt(t *testing.T) {
	got := userApprovalChoicePrompt("wireguard", []string{"david", "bob", "alice"})
	want := "This command requires approval for 'wireguard'. Select one approver: a) david, b) bob, c) alice"
	if got != want {
		t.Fatalf("userApprovalChoicePrompt() = %q, want %q", got, want)
	}
}
