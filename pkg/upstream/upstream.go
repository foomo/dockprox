package upstream

import (
	"context"
	"net"
)

// Dialer produces a net.Conn to the requested hostPort target, using the
// upstream's transport semantics. Implementations are safe for concurrent
// use.
type Dialer interface {
	// Dial returns a connection to hostPort ("host:port"). The caller owns
	// the returned conn and must Close it.
	Dial(ctx context.Context, hostPort string) (net.Conn, error)
	// Name returns the upstream's configured name for logging.
	Name() string
}
