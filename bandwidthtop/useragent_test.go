package bandwidthtop

import (
	"testing"

	"bandwidth-monitor/version"
)

func TestUserAgentUsesInjectedVersionAndDevelopmentFallback(t *testing.T) {
	originalVersion, originalCommit := version.Version, version.Commit
	t.Cleanup(func() {
		version.Version, version.Commit = originalVersion, originalCommit
	})
	tests := []struct {
		name, buildVersion, commit, want string
	}{
		{"package version", "0.0.0~git123.1.gabcdef", "abcdef", "bandwidth-top/0.0.0~git123.1.gabcdef"},
		{"commit build", "dev", "abcdef1", "bandwidth-top/0.0.0-git.abcdef1"},
		{"plain development", "dev", "unknown", "bandwidth-top/devel"},
		{"version header injection", "1.0\r\nX-Test: yes", "abcdef1", "bandwidth-top/devel"},
		{"commit header injection", "dev", "abc\r\nX-Test: yes", "bandwidth-top/devel"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			version.Version, version.Commit = test.buildVersion, test.commit
			if got := UserAgent(); got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
		})
	}
}
