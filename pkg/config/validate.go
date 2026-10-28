package config

import (
	"crypto/sha256"
	"encoding/base64"
	"maps"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/pkg/errors"
)

// Upstream type constants used by Upstream.Type.
const (
	UpstreamSocks5      = "socks5"
	UpstreamHTTP        = "http"
	UpstreamDirect      = "direct"
	UpstreamSSH         = "ssh"
	UpstreamForward     = "forward"
	UpstreamOpenConnect = "openconnect"
)

// Listener type constants used by Listener.Type. Empty means socks5.
const (
	ListenerSocks5 = "socks5"
	ListenerTCP    = "tcp"
)

// openConnectProtocols are the --protocol values openconnect accepts.
var openConnectProtocols = []string{"f5", "anyconnect", "nc", "gp", "pulse", "fortinet", "array"}

// IdentityAgentEnv is the sentinel identityAgent value meaning "read the
// socket path from $SSH_AUTH_SOCK", matching OpenSSH's directive.
const IdentityAgentEnv = "SSH_AUTH_SOCK"

// Validate checks that the configuration is internally consistent. Errors
// are wrapped with the offending field's path.
func (c *Config) Validate() error {
	if c.Listen == "" {
		return errors.New("listen: required")
	}

	if _, _, err := net.SplitHostPort(c.Listen); err != nil {
		return errors.Wrap(err, "listen")
	}

	switch c.LogLevel {
	case "", "debug", "info", "warn", "error":
		// empty is accepted; the loader (Defaults) supplies "info".
	default:
		return errors.Errorf("logLevel: unknown value %q", c.LogLevel)
	}

	for name, u := range c.Upstreams {
		if err := u.validate(); err != nil {
			return errors.Wrapf(err, "upstreams.%s", name)
		}
	}

	for i, r := range c.Rules {
		if r.Match == "" {
			return errors.Errorf("rules[%d].match: required", i)
		}

		stars := strings.Count(r.Match, "*")
		if stars > 1 || (stars == 1 && !strings.HasPrefix(r.Match, "*.")) {
			return errors.Errorf("rules[%d].match: invalid pattern %q", i, r.Match)
		}

		if _, ok := c.Upstreams[r.Upstream]; !ok {
			return errors.Errorf("rules[%d].upstream: unknown upstream %q", i, r.Upstream)
		}
	}

	referenced := make(map[string]struct{}, len(c.Rules))
	for _, r := range c.Rules {
		referenced[r.Upstream] = struct{}{}
	}

	seen := make(map[string]struct{})

	for i, l := range c.Listeners {
		path := "listeners[" + strconv.Itoa(i) + "]"

		if l.Listen == "" {
			return errors.Errorf("%s.listen: required", path)
		}

		if _, _, err := net.SplitHostPort(l.Listen); err != nil {
			return errors.Wrapf(err, "%s.listen", path)
		}

		if _, dup := seen[l.Listen]; dup {
			return errors.Errorf("%s.listen: duplicate %q", path, l.Listen)
		}

		seen[l.Listen] = struct{}{}

		switch l.Type {
		case "", ListenerSocks5:
			if l.Target != "" {
				return errors.Errorf("%s.target: only valid on type %q", path, ListenerTCP)
			}
		case ListenerTCP:
			host, _, err := net.SplitHostPort(l.Target)
			if err != nil {
				return errors.Wrapf(err, "%s.target", path)
			}

			if host == "" {
				return errors.Errorf("%s.target: host required, got %q", path, l.Target)
			}
		default:
			return errors.Errorf("%s.type: unknown %q", path, l.Type)
		}

		if l.Upstream == "" {
			continue
		}

		if _, ok := c.Upstreams[l.Upstream]; !ok {
			return errors.Errorf("%s.upstream: unknown upstream %q", path, l.Upstream)
		}

		referenced[l.Upstream] = struct{}{}
	}

	for _, name := range slices.Sorted(maps.Keys(c.Upstreams)) {
		via := c.Upstreams[name].Via
		if via == "" {
			continue
		}

		target, ok := c.Upstreams[via]
		if !ok {
			return errors.Errorf("upstreams.%s.via: unknown upstream %q", name, via)
		}

		if target.Type == UpstreamSSH {
			return errors.Errorf("upstreams.%s.via: cannot chain through ssh upstream %q", name, via)
		}

		referenced[via] = struct{}{}
	}

	// ssh and openconnect upstreams hold a connection or process; one that
	// no rule, listener or via uses is a config mistake.
	for _, name := range slices.Sorted(maps.Keys(c.Upstreams)) {
		if t := c.Upstreams[name].Type; t == UpstreamSSH || t == UpstreamOpenConnect {
			if _, ok := referenced[name]; !ok {
				return errors.Errorf("upstreams.%s: unreferenced %s upstream: no rule, listener or via uses it", name, t)
			}
		}
	}

	return nil
}

func (u Upstream) validate() error {
	switch u.Type {
	case UpstreamSocks5:
		if u.Addr == "" {
			return errors.New("addr: required for socks5")
		}
	case UpstreamHTTP:
		if u.URL == "" {
			return errors.New("url: required for http")
		}
	case UpstreamForward:
		if u.Addr == "" {
			return errors.New("addr: required for forward")
		}

		host, _, err := net.SplitHostPort(u.Addr)
		if err != nil {
			return errors.Wrap(err, "addr")
		}

		if host == "" {
			return errors.Errorf("addr: host required, got %q", u.Addr)
		}
	case UpstreamDirect:
		// no required fields
	case UpstreamSSH:
		if err := u.validateSSH(); err != nil {
			return err
		}
	case UpstreamOpenConnect:
		if err := u.validateOpenConnect(); err != nil {
			return err
		}
	default:
		return errors.Errorf("type: unknown %q", u.Type)
	}

	if u.Type != UpstreamSSH {
		if err := u.rejectSSHFields(); err != nil {
			return err
		}
	}

	if u.Type != UpstreamOpenConnect {
		if err := u.rejectOpenConnectFields(); err != nil {
			return err
		}
	}

	if u.DNS != "" && u.DNS != "local" && u.DNS != "remote" {
		return errors.Errorf("dns: unknown %q", u.DNS)
	}

	return nil
}

func (u Upstream) validateSSH() error {
	if u.Host == "" {
		return errors.New("host: required for ssh")
	}

	if u.Port != 0 && (u.Port < 1 || u.Port > 65535) {
		return errors.Errorf("port: out of range %d", u.Port)
	}

	if u.KeyFile == "" && u.IdentityAgent == "" {
		return errors.New("keyFile or identityAgent: at least one required for ssh")
	}

	if u.HostKey != "" {
		if err := validateFingerprint(u.HostKey); err != nil {
			return errors.Wrap(err, "hostKey")
		}
	}

	return nil
}

// rejectSSHFields fails loudly on ssh-only fields set on a non-ssh
// upstream; they would otherwise be silently ignored.
func (u Upstream) rejectSSHFields() error {
	for _, f := range []struct {
		name string
		set  bool
	}{
		{"host", u.Host != ""},
		{"port", u.Port != 0},
		{"user", u.User != ""},
		{"keyFile", u.KeyFile != ""},
		{"keyFilePassphrase", u.KeyFilePassphrase != ""},
		{"identityAgent", u.IdentityAgent != ""},
		{"hostKey", u.HostKey != ""},
		{"via", u.Via != ""},
	} {
		if f.set {
			return errors.Errorf("%s: only valid on type %q", f.name, UpstreamSSH)
		}
	}

	return nil
}

func (u Upstream) validateOpenConnect() error {
	if u.Server == "" {
		return errors.New("server: required for openconnect")
	}

	parsed, err := url.Parse(u.Server)
	if err != nil {
		return errors.Wrap(err, "server")
	}

	if parsed.Scheme != "https" || parsed.Host == "" {
		return errors.Errorf("server: expected https://host, got %q", u.Server)
	}

	if u.Protocol != "" && !slices.Contains(openConnectProtocols, u.Protocol) {
		return errors.Errorf("protocol: unknown %q", u.Protocol)
	}

	if u.Auth == nil || u.Auth.Username == "" {
		return errors.New("auth.username: required for openconnect")
	}

	return nil
}

// rejectOpenConnectFields fails loudly on openconnect-only fields set on
// another upstream type.
func (u Upstream) rejectOpenConnectFields() error {
	for _, f := range []struct {
		name string
		set  bool
	}{
		{"server", u.Server != ""},
		{"protocol", u.Protocol != ""},
		{"serverCert", u.ServerCert != ""},
		{"tun", u.Tun},
	} {
		if f.set {
			return errors.Errorf("%s: only valid on type %q", f.name, UpstreamOpenConnect)
		}
	}

	return nil
}

// validateFingerprint checks the SHA256:<base64> form printed by
// `ssh-keygen -lf`.
func validateFingerprint(s string) error {
	raw, ok := strings.CutPrefix(s, "SHA256:")
	if !ok {
		return errors.Errorf("expected SHA256:<base64>, got %q", s)
	}

	sum, err := base64.RawStdEncoding.DecodeString(raw)
	if err != nil {
		return errors.Wrap(err, "decode base64")
	}

	if len(sum) != sha256.Size {
		return errors.Errorf("expected %d digest bytes, got %d", sha256.Size, len(sum))
	}

	return nil
}
