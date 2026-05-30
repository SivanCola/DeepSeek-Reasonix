package permission

import "testing"

func TestIsReadOnlyBashSubject(t *testing.T) {
	tests := []struct {
		cmd  string
		want bool
	}{
		// Read-only commands
		{"ls", true},
		{"ls /tmp", true},
		{"cat main.go", true},
		{"head -n 5 file.txt", true},
		{"find . -name '*.go'", true},
		{"grep TODO *.go", true},
		{"rg pattern", true},
		{"echo hello", true},
		{"pwd", true},
		{"whoami", true},
		{"wc -l file.txt", true},
		{"stat main.go", true},
		{"du -sh .", true},
		{"diff a.go b.go", true},

		// Git read-only
		{"git log", true},
		{"git status", true},
		{"git diff", true},
		{"git show HEAD", true},
		{"git branch", true},
		{"git blame main.go", true},

		// Go read-only
		{"go vet ./...", true},
		{"go doc fmt", true},
		{"go list ./...", true},

		// Not read-only
		{"rm file.txt", false},
		{"rm -rf /", false},
		{"git commit -m 'msg'", false},
		{"git push", false},
		{"git push --force", false},
		{"go build ./...", false},
		{"go test ./...", false},
		{"make build", false},
		{"curl https://example.com", false},
		{"npm install", false},
		{"chmod 777 file", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.cmd, func(t *testing.T) {
			if got := isReadOnlyBashSubject(tt.cmd); got != tt.want {
				t.Errorf("isReadOnlyBashSubject(%q) = %v, want %v", tt.cmd, got, tt.want)
			}
		})
	}
}

func TestBashDangerWarning(t *testing.T) {
	tests := []struct {
		cmd  string
		want string
	}{
		{"ls", ""},
		{"cat main.go", ""},
		{"rm -rf /tmp/build", "recursive delete"},
		{"rm -r old_files", "recursive delete"},
		{"git push --force origin main", "force push"},
		{"git push -f", "force push"},
		{"git reset --hard HEAD~1", "hard reset"},
		{"chmod 777 script.sh", "world-writable"},
		{"sudo make install", "superuser"},
		{"git clean -fd", "force clean"},
	}

	for _, tt := range tests {
		t.Run(tt.cmd, func(t *testing.T) {
			if got := BashDangerWarning(tt.cmd); got != tt.want {
				t.Errorf("BashDangerWarning(%q) = %q, want %q", tt.cmd, got, tt.want)
			}
		})
	}
}