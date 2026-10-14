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

func TestValidate_ForwardOK(t *testing.T) {
	c := cfgWith(
		map[string]config.Upstream{"cluster-a": {Type: "forward", Addr: "127.0.0.1:10310"}},
		config.Rule{Match: "*.a.local.gd", Upstream: "cluster-a"},
	)
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
		{"forward without addr", cfgWith(
			map[string]config.Upstream{"c": {Type: "forward"}},
		), "addr"},
		{"forward with malformed addr", cfgWith(
			map[string]config.Upstream{"c": {Type: "forward", Addr: "127.0.0.1"}},
		), "addr"},
		{"forward without host", cfgWith(
			map[string]config.Upstream{"c": {Type: "forward", Addr: ":10310"}},
		), "host required"},
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
			map[string]config.Upstream{"j": {Type: "ssh", KeyFile: "/tmp/id"}},
		), "host"},
		{"ssh without auth", cfgWith(
			map[string]config.Upstream{"j": {Type: "ssh", Host: "b.example.com"}},
		), "keyFile"},
		{"malformed hostKey", cfgWith(
			map[string]config.Upstream{"j": sshUp(func(u *config.Upstream) {
				u.HostKey = "MD5:aa:bb"
			})},
		), "hostKey"},
		{"port out of range", cfgWith(
			map[string]config.Upstream{"j": sshUp(func(u *config.Upstream) {
				u.Port = 70000
			})},
		), "port"},
		{"ssh field on http upstream", cfgWith(
			map[string]config.Upstream{"c": {Type: "http", URL: "http://p:3128", Host: "nope"}},
		), "host"},
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
		{"tunnel with listener", config.Config{
			Listen:    "127.0.0.1:8888",
			Upstreams: map[string]config.Upstream{"j": sshUp(nil)},
			Listeners: []config.Listener{{Listen: "127.0.0.1:1080", Upstream: "j"}},
		}},
		{"tunnel referenced by rule", cfgWith(
			map[string]config.Upstream{"j": sshUp(nil)},
			config.Rule{Match: "*.internal.example.com", Upstream: "j"},
		)},
		{"agent auth and pinned host key", cfgWith(
			map[string]config.Upstream{"j": {
				Type: "ssh", Host: "b.example.com", Port: 2222, User: "deploy",
				IdentityAgent: "SSH_AUTH_SOCK",
				HostKey:       "SHA256:" + base64.RawStdEncoding.EncodeToString(make([]byte, 32)),
			}},
			config.Rule{Match: "*.internal.example.com", Upstream: "j"},
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

func ocUp(mut func(*config.Upstream)) config.Upstream {
	u := config.Upstream{
		Type: "openconnect", Server: "https://vpn.example.com",
		Auth: &config.UpstreamAuth{Username: "u", Password: "p"},
	}
	if mut != nil {
		mut(&u)
	}

	return u
}

func TestValidate_OpenConnectOK(t *testing.T) {
	good := []struct {
		name string
		c    config.Config
	}{
		{"vpn referenced by rule", cfgWith(
			map[string]config.Upstream{"vpn": ocUp(nil)},
			config.Rule{Match: "*.internal", Upstream: "vpn"},
		)},
		{"ssh via vpn", cfgWith(map[string]config.Upstream{
			"vpn": ocUp(func(u *config.Upstream) { u.Protocol = "f5"; u.ServerCert = "pin-sha256:abc" }),
			"j":   sshUp(func(u *config.Upstream) { u.Via = "vpn" }),
		}, config.Rule{Match: "*.internal", Upstream: "j"})},
	}
	for _, tc := range good {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.c.Validate(); err != nil {
				t.Fatalf("Validate: %v", err)
			}
		})
	}
}

func TestValidate_OpenConnectErrors(t *testing.T) {
	rule := config.Rule{Match: "*.internal", Upstream: "vpn"}

	bad := []struct {
		name string
		c    config.Config
		msg  string
	}{
		{"no server", cfgWith(
			map[string]config.Upstream{"vpn": ocUp(func(u *config.Upstream) { u.Server = "" })}, rule,
		), "server"},
		{"http server", cfgWith(
			map[string]config.Upstream{"vpn": ocUp(func(u *config.Upstream) { u.Server = "http://vpn" })}, rule,
		), "https"},
		{"unknown protocol", cfgWith(
			map[string]config.Upstream{"vpn": ocUp(func(u *config.Upstream) { u.Protocol = "ipsec" })}, rule,
		), "protocol"},
		{"no username", cfgWith(
			map[string]config.Upstream{"vpn": ocUp(func(u *config.Upstream) { u.Auth = nil })}, rule,
		), "auth.username"},
		{"unreferenced", cfgWith(
			map[string]config.Upstream{"vpn": ocUp(nil)},
		), "unreferenced"},
		{"server on socks5", cfgWith(
			map[string]config.Upstream{"j": {Type: "socks5", Addr: "127.0.0.1:1", Server: "https://x"}},
		), "server"},
		{"via on socks5", cfgWith(
			map[string]config.Upstream{"j": {Type: "socks5", Addr: "127.0.0.1:1", Via: "x"}},
		), "via"},
		{"via unknown", cfgWith(
			map[string]config.Upstream{"j": sshUp(func(u *config.Upstream) { u.Via = "ghost" })},
		), "ghost"},
		{"via ssh", cfgWith(map[string]config.Upstream{
			"a": sshUp(nil),
			"b": sshUp(func(u *config.Upstream) { u.Via = "a" }),
		}), "chain"},
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

func TestValidate_Listeners(t *testing.T) {
	ups := map[string]config.Upstream{"j": sshUp(nil)}

	good := []config.Config{
		{Listen: "127.0.0.1:8888", Upstreams: ups, Listeners: []config.Listener{{Listen: "127.0.0.1:1080", Upstream: "j"}}},
		{Listen: "127.0.0.1:8888", Listeners: []config.Listener{{Listen: "127.0.0.1:1090"}}},
	}
	for i, c := range good {
		if err := c.Validate(); err != nil {
			t.Fatalf("good[%d]: %v", i, err)
		}
	}

	bad := []struct {
		name string
		c    config.Config
		msg  string
	}{
		{"no listen", config.Config{Listen: "127.0.0.1:8888", Listeners: []config.Listener{{}}}, "listeners[0].listen"},
		{"bad listen", config.Config{Listen: "127.0.0.1:8888", Listeners: []config.Listener{{Listen: "nope"}}}, "listeners[0].listen"},
		{"unknown upstream", config.Config{
			Listen: "127.0.0.1:8888", Listeners: []config.Listener{{Listen: "127.0.0.1:1080", Upstream: "ghost"}},
		}, "ghost"},
		{"duplicate", config.Config{
			Listen:    "127.0.0.1:8888",
			Listeners: []config.Listener{{Listen: "127.0.0.1:1080"}, {Listen: "127.0.0.1:1080"}},
		}, "listeners[1].listen: duplicate"},
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

func TestLoad_RejectsRemovedSocks5Listen(t *testing.T) {
	_, err := config.LoadBytes([]byte(`
upstreams:
  j:
    type: ssh
    host: b.example.com
    keyFile: /tmp/id
    socks5Listen: 127.0.0.1:1080
`))
	if err == nil || !strings.Contains(err.Error(), "upstreams.j.socks5Listen: removed") {
		t.Fatalf("err=%v, want socks5Listen removal error", err)
	}
}

func TestValidate_TCPListener(t *testing.T) {
	ups := map[string]config.Upstream{"j": sshUp(nil)}

	ok := config.Config{Listen: "127.0.0.1:8888", Upstreams: ups, Listeners: []config.Listener{
		{Type: "tcp", Listen: "127.0.0.1:5432", Upstream: "j", Target: "db.internal:5432"},
	}}
	if err := ok.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	for _, tc := range []struct {
		name string
		l    config.Listener
		msg  string
	}{
		{"tcp without target", config.Listener{Type: "tcp", Listen: "127.0.0.1:5432"}, "listeners[0].target"},
		{"tcp target without host", config.Listener{Type: "tcp", Listen: "127.0.0.1:5432", Target: ":5432"}, "host required"},
		{"target on socks5", config.Listener{Listen: "127.0.0.1:1080", Target: "db:5432"}, "only valid on type"},
		{"unknown type", config.Listener{Type: "udp", Listen: "127.0.0.1:1080"}, "listeners[0].type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := config.Config{Listen: "127.0.0.1:8888", Listeners: []config.Listener{tc.l}}

			err := c.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.msg) {
				t.Fatalf("err=%v want substring %q", err, tc.msg)
			}
		})
	}
}
