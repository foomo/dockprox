package config

import (
	"crypto/sha256"
	"encoding/base64"
	"maps"
	"net"
	"slices"
	"strings"

	"github.com/pkg/errors"
)

// Upstream type constants used by Upstream.Type.
const (
	UpstreamSocks5 = "socks5"
	UpstreamHTTP   = "http"
	UpstreamDirect = "direct"
	UpstreamSSH    = "ssh"
)

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

	seen := make(map[string]string, len(c.Upstreams))

	referenced := make(map[string]struct{}, len(c.Rules))
	for _, r := range c.Rules {
		referenced[r.Upstream] = struct{}{}
	}

	for _, name := range slices.Sorted(maps.Keys(c.Upstreams)) {
		u := c.Upstreams[name]

		if u.Socks5Listen != "" {
			if other, dup := seen[u.Socks5Listen]; dup {
				return errors.Errorf("upstreams.%s.socks5Listen: duplicate of upstreams.%s (%q)",
					name, other, u.Socks5Listen)
			}

			seen[u.Socks5Listen] = name
		}

		if u.Type == UpstreamSSH && u.Socks5Listen == "" {
			if _, ok := referenced[name]; !ok {
				return errors.Errorf("upstreams.%s: unreferenced ssh upstream with no socks5Listen", name)
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
	case UpstreamDirect:
		// no required fields
	case UpstreamSSH:
		if err := u.validateSSH(); err != nil {
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

	if u.Socks5Listen != "" {
		if _, _, err := net.SplitHostPort(u.Socks5Listen); err != nil {
			return errors.Wrap(err, "socks5Listen")
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
		{"socks5Listen", u.Socks5Listen != ""},
	} {
		if f.set {
			return errors.Errorf("%s: only valid on type %q", f.name, UpstreamSSH)
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
