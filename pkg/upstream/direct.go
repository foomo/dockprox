package upstream

import (
	"context"
	"net"
)

// DirectDialer dials the target hostPort from the local host. It uses the
// system resolver.
type DirectDialer struct {
	name string
	d    net.Dialer
}

// NewDirect returns a DirectDialer with the given name.
func NewDirect(name string) *DirectDialer {
	return &DirectDialer{name: name}
}

// Dial implements Dialer.
func (d *DirectDialer) Dial(ctx context.Context, hostPort string) (net.Conn, error) {
	return d.d.DialContext(ctx, "tcp", hostPort)
}

// Name implements Dialer.
func (d *DirectDialer) Name() string { return d.name }
