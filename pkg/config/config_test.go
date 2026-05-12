package config_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/foomo/dockprox/pkg/config"
)

func TestValidate_OK(t *testing.T) {
	c := &config.Config{
		Listen:   "127.0.0.1:8888",
		LogLevel: "info",
		Upstreams: map[string]config.Upstream{
			"jump": {Type: "socks5", Addr: "127.0.0.1:1080", DNS: "remote"},
		},
		Rules: []config.Rule{
			{Match: "*.azurecr.io", Upstream: "jump"},
		},
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidate_Errors(t *testing.T) {
	bad := []struct {
		name string
		c    config.Config
		msg  string
	}{
		{"bad listen", config.Config{Listen: "not-an-addr"}, "listen"},
		{"missing upstream addr", config.Config{
			Listen:    "127.0.0.1:8888",
			Upstreams: map[string]config.Upstream{"j": {Type: "socks5"}},
		}, "addr"},
		{"unknown rule upstream", config.Config{
			Listen:    "127.0.0.1:8888",
			Upstreams: map[string]config.Upstream{"j": {Type: "socks5", Addr: "127.0.0.1:1080"}},
			Rules:     []config.Rule{{Match: "x.io", Upstream: "ghost"}},
		}, "ghost"},
		{"unknown type", config.Config{
			Listen:    "127.0.0.1:8888",
			Upstreams: map[string]config.Upstream{"j": {Type: "weird"}},
		}, "type"},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.c.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.msg) {
				t.Fatalf("err=%v want substring %q", err, tc.msg)
			}
		})
	}
}

func TestLoad_FromBytes(t *testing.T) {
	yml := []byte(`
listen: 127.0.0.1:9999
logLevel: debug
upstreams:
  j:
    type: socks5
    addr: 127.0.0.1:1080
    dns: remote
rules:
  - match: "*.azurecr.io"
    upstream: j
`)

	c, err := config.LoadBytes(yml)
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}

	if c.Listen != "127.0.0.1:9999" {
		t.Fatalf("listen=%q", c.Listen)
	}

	if c.Upstreams["j"].DNS != "remote" {
		t.Fatalf("dns=%q", c.Upstreams["j"].DNS)
	}
}

func TestLoad_FromFile(t *testing.T) {
	dir := t.TempDir()

	p := filepath.Join(dir, "d.yaml")
	if err := os.WriteFile(p, []byte("listen: 127.0.0.1:1234\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	c, err := config.LoadFile(p)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}

	if c.Listen != "127.0.0.1:1234" {
		t.Fatalf("listen=%q", c.Listen)
	}
}

func TestLoad_Reader(t *testing.T) {
	c, err := config.Load(bytes.NewReader([]byte("listen: 127.0.0.1:7777\n")))
	if err != nil {
		t.Fatal(err)
	}

	if c.Listen != "127.0.0.1:7777" {
		t.Fatalf("listen=%q", c.Listen)
	}
}

func TestLoad_AppliesDefaults(t *testing.T) {
	// YAML omits both listen and logLevel; defaults should fill them.
	c, err := config.LoadBytes([]byte("rules: []\n"))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}

	if c.Listen != "127.0.0.1:8888" {
		t.Fatalf("listen=%q, want default", c.Listen)
	}

	if c.LogLevel != "info" {
		t.Fatalf("logLevel=%q, want default", c.LogLevel)
	}
}
