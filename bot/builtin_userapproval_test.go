package bot

import (
	"reflect"
	"testing"
)

func TestEffectiveApprovalApprovers(t *testing.T) {
	cfg := userApprovalConfig{
		FallbackApprovers: []string{"david", "alice"},
		PluginApprovers: map[string]userApprovalPluginApprovers{
			"wireguard": {
				Approvers:    []string{" Alice ", "bob", "alice", "david", ""},
				hasApprovers: true,
			},
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

func TestEffectiveApprovalPolicyStrictDefaults(t *testing.T) {
	cfg := userApprovalConfig{
		FallbackApprovers: []string{"alice", "bob"},
	}

	got := effectiveApprovalPolicy(cfg, "", "alice")
	wantApprovers := []string{"bob"}
	if !got.strict || !got.requesterListed || !reflect.DeepEqual(got.approvers, wantApprovers) {
		t.Fatalf("effectiveApprovalPolicy default strict = %#v, want strict true requesterListed true approvers %#v", got, wantApprovers)
	}
}

func TestEffectiveApprovalPolicyNonStrictDefault(t *testing.T) {
	defaultStrict := false
	cfg := userApprovalConfig{
		DefaultStrict:     &defaultStrict,
		FallbackApprovers: []string{"alice", "bob"},
	}

	got := effectiveApprovalPolicy(cfg, "", "alice")
	wantApprovers := []string{"bob"}
	if got.strict || !got.requesterListed || !reflect.DeepEqual(got.approvers, wantApprovers) {
		t.Fatalf("effectiveApprovalPolicy non-strict default = %#v, want strict false requesterListed true approvers %#v", got, wantApprovers)
	}
}

func TestEffectiveApprovalPolicyPluginStrictOverride(t *testing.T) {
	defaultStrict := true
	pluginStrict := false
	cfg := userApprovalConfig{
		DefaultStrict:     &defaultStrict,
		FallbackApprovers: []string{"carol"},
		PluginApprovers: map[string]userApprovalPluginApprovers{
			"wireguard": {
				Approvers:    []string{"alice", "bob"},
				Strict:       &pluginStrict,
				hasApprovers: true,
			},
		},
	}

	got := effectiveApprovalPolicy(cfg, "wireguard", "alice")
	wantApprovers := []string{"bob"}
	if got.strict || !got.requesterListed || !reflect.DeepEqual(got.approvers, wantApprovers) {
		t.Fatalf("effectiveApprovalPolicy plugin override = %#v, want strict false requesterListed true approvers %#v", got, wantApprovers)
	}
}

func TestEffectiveApprovalPolicyPluginStrictOverrideUsesFallbackApprovers(t *testing.T) {
	defaultStrict := true
	pluginStrict := false
	cfg := userApprovalConfig{
		DefaultStrict:     &defaultStrict,
		FallbackApprovers: []string{"alice", "bob"},
		PluginApprovers: map[string]userApprovalPluginApprovers{
			"wireguard": {
				Strict: &pluginStrict,
			},
		},
	}

	got := effectiveApprovalPolicy(cfg, "wireguard", "alice")
	wantApprovers := []string{"bob"}
	if got.strict || !got.requesterListed || !reflect.DeepEqual(got.approvers, wantApprovers) {
		t.Fatalf("effectiveApprovalPolicy plugin strict fallback = %#v, want strict false requesterListed true approvers %#v", got, wantApprovers)
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
	got := userApprovalChoicePrompt("wireguard/add-device", []string{"david", "bob", "alice"})
	want := "Approval required for command wireguard/add-device. Select one approver: a) david, b) bob, c) alice"
	if got != want {
		t.Fatalf("userApprovalChoicePrompt() = %q, want %q", got, want)
	}
}

func TestUserApprovalActionName(t *testing.T) {
	for _, tc := range []struct {
		name     string
		pipeName string
		command  string
		want     string
	}{
		{"plugin command", "vpn", "add-device", "vpn/add-device"},
		{"pipeline fallback", "maintenance", "", "maintenance"},
		{"command fallback", "", "approve", "approve"},
		{"trimmed", " vpn ", " add-device ", "vpn/add-device"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := userApprovalActionName(tc.pipeName, tc.command)
			if got != tc.want {
				t.Fatalf("userApprovalActionName(%q, %q) = %q, want %q", tc.pipeName, tc.command, got, tc.want)
			}
		})
	}
}
