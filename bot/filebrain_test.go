package bot

import "testing"

func TestValidateFileBrainKey(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{name: "plain key", in: "alpha_1:beta"},
		{name: "hyphenated key", in: "alpha-1:beta"},
		{name: "hyphenated internal history key", in: "bot:histories:builtin-jobcmd"},
		{name: "hyphenated history lookup key", in: "bot:histories-lookup"},
		{name: "slashes are invalid", in: "alpha/beta", wantErr: true},
		{name: "backslashes are invalid", in: `alpha\\beta`, wantErr: true},
		{name: "empty key", in: "", wantErr: true},
		{name: "dotdot key", in: "..", wantErr: true},
		{name: "dotdot in key", in: "alpha..beta", wantErr: true},
		{name: "path traversal style", in: "alpha/../beta", wantErr: true},
		{name: "spaces disallowed", in: "alpha beta", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateFileBrainKey(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
