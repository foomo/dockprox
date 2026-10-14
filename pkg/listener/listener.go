package listener

import (
	"context"

	"github.com/charmbracelet/log"
	"github.com/foomo/dockprox/pkg/config"
	"github.com/foomo/dockprox/pkg/match"
	"github.com/foomo/dockprox/pkg/portforward"
	"github.com/foomo/dockprox/pkg/socks5server"
	"github.com/foomo/dockprox/pkg/upstream"
	"github.com/pkg/errors"
)

// Server is a bound listener. Serve blocks until ctx is cancelled or the
// listener closes.
type Server interface {
	Serve(ctx context.Context) error
	Addr() string
	Close() error
}

// New binds l. A pinned listener uses its upstream's dialer from reg; an
// unpinned one routes by m. ctx is used only for binding.
func New(ctx context.Context, l config.Listener, reg *upstream.Registry, m *match.Matcher, logger *log.Logger) (Server, error) {
	var d upstream.Dialer

	if l.Upstream != "" {
		var ok bool
		if d, ok = reg.Get(l.Upstream); !ok {
			return nil, errors.Errorf("upstream %q: not in registry", l.Upstream)
		}
	}

	switch l.Type {
	case "", config.ListenerSocks5:
		return socks5server.NewServer(ctx, socks5server.Options{
			Listen: l.Listen, Dialer: d, Matcher: m, Registry: reg, Logger: logger,
		})
	case config.ListenerTCP:
		return portforward.NewServer(ctx, portforward.Options{
			Listen: l.Listen, Target: l.Target, Dialer: d, Matcher: m, Registry: reg, Logger: logger,
		})
	default:
		return nil, errors.Errorf("listener type %q: unknown", l.Type)
	}
}
