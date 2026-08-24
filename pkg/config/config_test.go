package config_test

import (
	"bytes"
	"encoding/base64"
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

func sshUp(mut func(*config.Upstream)) config.Upstream {
	u := config.Upstream{Type: "ssh", Host: "bastion.example.com", KeyFile: "/tmp/id"}
	if mut != nil {
		mut(&u)
	}

	return u
}

func cfgWith(ups map[string]config.Upstream, rules ...config.Rule) config.Config {
	return config.Config{Listen: "127.0.0.1:8888", Upstreams: ups, Rules: rules}
}

func TestValidate_SSHErrors(t *testing.T) {
	bad := []struct {
		name string
		c    config.Config
		msg  string
	}{
		{"ssh without host", cfgWith(
			map[string]config.Upstream{"j": {Type: "ssh", KeyFile: "/tmp/id", Socks5Listen: "127.0.0.1:1080"}},
		), "host"},
		{"ssh without auth", cfgWith(
			map[string]config.Upstream{"j": {Type: "ssh", Host: "b.example.com", Socks5Listen: "127.0.0.1:1080"}},
		), "keyFile"},
		{"malformed hostKey", cfgWith(
			map[string]config.Upstream{"j": sshUp(func(u *config.Upstream) {
				u.HostKey = "MD5:aa:bb"
				u.Socks5Listen = "127.0.0.1:1080"
			})},
		), "hostKey"},
		{"port out of range", cfgWith(
			map[string]config.Upstream{"j": sshUp(func(u *config.Upstream) {
				u.Port = 70000
				u.Socks5Listen = "127.0.0.1:1080"
			})},
		), "port"},
		{"ssh field on http upstream", cfgWith(
			map[string]config.Upstream{"c": {Type: "http", URL: "http://p:3128", Host: "nope"}},
		), "host"},
		{"socks5Listen on http upstream", cfgWith(
			map[string]config.Upstream{"c": {Type: "http", URL: "http://p:3128", Socks5Listen: "127.0.0.1:1080"}},
		), "socks5Listen"},
		{"bad socks5Listen", cfgWith(
			map[string]config.Upstream{"j": sshUp(func(u *config.Upstream) { u.Socks5Listen = "not-an-addr" })},
		), "socks5Listen"},
		{"duplicate socks5Listen", cfgWith(map[string]config.Upstream{
			"a": sshUp(func(u *config.Upstream) { u.Socks5Listen = "127.0.0.1:1080" }),
			"b": sshUp(func(u *config.Upstream) { u.Socks5Listen = "127.0.0.1:1080" }),
		}), "duplicate"},
		{"dead tunnel config", cfgWith(
			map[string]config.Upstream{"j": sshUp(nil)},
		), "unreferenced"},
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

func TestValidate_SSHOK(t *testing.T) {
	good := []struct {
		name string
		c    config.Config
	}{
		{"tunnel with listener", cfgWith(
			map[string]config.Upstream{"j": sshUp(func(u *config.Upstream) { u.Socks5Listen = "127.0.0.1:1080" })},
		)},
		{"tunnel referenced by rule", cfgWith(
			map[string]config.Upstream{"j": sshUp(nil)},
			config.Rule{Match: "*.internal.example.com", Upstream: "j"},
		)},
		{"agent auth and pinned host key", cfgWith(
			map[string]config.Upstream{"j": {
				Type: "ssh", Host: "b.example.com", Port: 2222, User: "deploy",
				IdentityAgent: "SSH_AUTH_SOCK",
				HostKey:       "SHA256:" + base64.RawStdEncoding.EncodeToString(make([]byte, 32)),
				Socks5Listen:  "127.0.0.1:1080",
			}},
		)},
	}
	for _, tc := range good {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.c.Validate(); err != nil {
				t.Fatalf("Validate: %v", err)
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
