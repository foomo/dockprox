package config

import (
	"net"
	"strings"

	"github.com/pkg/errors"
)

// Upstream type constants used by Upstream.Type.
const (
	UpstreamSocks5 = "socks5"
	UpstreamHTTP   = "http"
	UpstreamDirect = "direct"
)

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
	default:
		return errors.Errorf("type: unknown %q", u.Type)
	}

	if u.DNS != "" && u.DNS != "local" && u.DNS != "remote" {
		return errors.Errorf("dns: unknown %q", u.DNS)
	}

	return nil
}
