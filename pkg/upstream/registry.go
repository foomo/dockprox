package upstream

import (
	"github.com/foomo/dockprox/pkg/config"
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

	for name, u := range cfg.Upstreams {
		d, err := build(name, u)
		if err != nil {
			return nil, errors.Wrapf(err, "upstream %q", name)
		}

		r.byName[name] = d
	}

	return r, nil
}

// Get returns the Dialer with the given name, or false if not found.
func (r *Registry) Get(name string) (Dialer, bool) {
	d, ok := r.byName[name]
	return d, ok
}

// Direct returns the built-in direct dialer.
func (r *Registry) Direct() Dialer { return r.direct }

func build(name string, u config.Upstream) (Dialer, error) {
	switch u.Type {
	case config.UpstreamDirect:
		return NewDirect(name), nil
	case config.UpstreamSocks5:
		return NewSocks5(name, u), nil
	case config.UpstreamHTTP:
		return NewHTTP(name, u)
	default:
		return nil, errors.Errorf("unknown type %q", u.Type)
	}
}
