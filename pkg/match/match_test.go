package match_test

import (
	"testing"

	"github.com/foomo/dockprox/pkg/match"
)

func TestMatcher_First(t *testing.T) {
	tests := []struct {
		name      string
		rules     []match.Rule
		host      string
		wantRule  string
		wantFound bool
	}{
		{"exact hit", []match.Rule{{Pattern: "ghcr.io", Upstream: "jump"}}, "ghcr.io", "ghcr.io", true},
		{"suffix hit", []match.Rule{{Pattern: "*.azurecr.io", Upstream: "jump"}}, "foo.azurecr.io", "*.azurecr.io", true},
		{"suffix nested", []match.Rule{{Pattern: "*.azurecr.io", Upstream: "jump"}}, "a.b.azurecr.io", "*.azurecr.io", true},
		{"case insensitive", []match.Rule{{Pattern: "GHCR.io", Upstream: "jump"}}, "ghcr.IO", "GHCR.io", true},
		{"port stripped", []match.Rule{{Pattern: "ghcr.io", Upstream: "jump"}}, "ghcr.io:443", "ghcr.io", true},
		{"ipv6 port stripped", []match.Rule{{Pattern: "::1", Upstream: "jump"}}, "[::1]:443", "::1", true},
		{"no match", []match.Rule{{Pattern: "ghcr.io", Upstream: "jump"}}, "docker.io", "", false},
		{"order wins", []match.Rule{
			{Pattern: "*.azurecr.io", Upstream: "a"},
			{Pattern: "foo.azurecr.io", Upstream: "b"},
		}, "foo.azurecr.io", "*.azurecr.io", true},
	}

	known := map[string]struct{}{"jump": {}, "a": {}, "b": {}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := match.New(tt.rules, known)
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			got, ok := m.First(tt.host)
			if ok != tt.wantFound {
				t.Fatalf("found=%v want %v", ok, tt.wantFound)
			}

			if ok && got.Pattern != tt.wantRule {
				t.Fatalf("pattern=%q want %q", got.Pattern, tt.wantRule)
			}
		})
	}
}

func TestMatcher_NewValidation(t *testing.T) {
	known := map[string]struct{}{"jump": {}}

	cases := []struct {
		name  string
		rules []match.Rule
	}{
		{"empty pattern", []match.Rule{{Pattern: "", Upstream: "jump"}}},
		{"multi-star", []match.Rule{{Pattern: "*.foo.*", Upstream: "jump"}}},
		{"middle star", []match.Rule{{Pattern: "foo.*.bar", Upstream: "jump"}}},
		{"unknown upstream", []match.Rule{{Pattern: "ghcr.io", Upstream: "ghost"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := match.New(tc.rules, known); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}
