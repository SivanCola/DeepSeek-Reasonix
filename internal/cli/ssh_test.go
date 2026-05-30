package cli

import "testing"

func TestParseSSHURI(t *testing.T) {
	tests := []struct {
		raw  string
		want *SSHConfig
		err  bool
	}{
		{"ssh://host/path", &SSHConfig{Host: "host", Port: "22", RemotePath: "path"}, false},
		{"ssh://user@host:2222/path", &SSHConfig{User: "user", Host: "host", Port: "2222", RemotePath: "path"}, false},
		{"ssh://host", &SSHConfig{Host: "host", Port: "22"}, false},
		{"http://host", nil, true},
		{"not-a-uri", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			got, err := parseSSHURI(tt.raw)
			if tt.err && err == nil {
				t.Fatal("expected error")
			}
			if !tt.err && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != nil && tt.want != nil {
				if got.Host != tt.want.Host || got.Port != tt.want.Port || got.RemotePath != tt.want.RemotePath || got.User != tt.want.User {
					t.Errorf("got %+v, want %+v", got, tt.want)
				}
			}
		})
	}
}
