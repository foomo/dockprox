package upstream

import (
	"io"

	"github.com/foomo/dockprox/pkg/config"
	"github.com/foomo/dockprox/pkg/secret"
	"github.com/pkg/errors"
)

// Registry holds the named Dialers built from a config.
type Registry struct {
	byName map[string]Dialer
	direct Dialer
}

// NewRegistry builds a Registry from the upstreams map in cfg. The returned
// Registry always has a built-in direct dialer accessible via Direct().
func NewRegistry(cfg *config.Config) (*Registry, error) {
	r := &Registry{
		byName: make(map[string]Dialer),
		direct: NewDirect("direct"),
	}

	// ssh upstreams are built last so the upstream named by via exists.
	for _, sshPass := range []bool{false, true} {
		for name, u := range cfg.Upstreams {
			if (u.Type == config.UpstreamSSH) != sshPass {
				continue
			}

			d, err := r.build(name, u)
			if err != nil {
				_ = r.Close()
				return nil, errors.Wrapf(err, "upstream %q", name)
			}

			r.byName[name] = d
		}
	}

	return r, nil
}

// Close releases every upstream holding a connection or process, such as
// SSH connections and VPNs. The Registry must not be used afterwards.
func (r *Registry) Close() error {
	var first error

	for _, d := range r.byName {
		if c, ok := d.(io.Closer); ok {
			if err := c.Close(); err != nil && first == nil {
				first = err
			}
		}
	}

	return first
}

// Get returns the Dialer with the given name, or false if not found.
func (r *Registry) Get(name string) (Dialer, bool) {
	d, ok := r.byName[name]
	return d, ok
}

// Direct returns the built-in direct dialer.
func (r *Registry) Direct() Dialer { return r.direct }

func (r *Registry) build(name string, u config.Upstream) (Dialer, error) {
	// Secrets resolve lazily; fail at startup at least when the tool for
	// one is missing.
	refs := []string{u.KeyFilePassphrase}
	if u.Auth != nil {
		refs = append(refs, u.Auth.Username, u.Auth.Password)
	}

	for _, ref := range refs {
		if err := secret.Check(ref); err != nil {
			return nil, err
		}
	}

	switch u.Type {
	case config.UpstreamDirect:
		return NewDirect(name), nil
	case config.UpstreamSocks5:
		return NewSocks5(name, u), nil
	case config.UpstreamHTTP:
		return NewHTTP(name, u)
	case config.UpstreamForward:
		return NewForward(name, u.Addr), nil
	case config.UpstreamSSH:
		var via Dialer
		if u.Via != "" {
			via = r.byName[u.Via]
		}

		return NewSSH(name, u, via)
	case config.UpstreamOpenConnect:
		return NewOpenConnect(name, u)
	default:
		return nil, errors.Errorf("unknown type %q", u.Type)
	}
}
