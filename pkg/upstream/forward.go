package upstream

import (
	"context"
	"net"
)

// ForwardDialer ignores the requested target and always dials a fixed
// addr. It is the "send matched hosts somewhere else" primitive: the
// client's hostname survives in the TLS SNI and the Host header, so a
// vhost-routing backend (ingress controller, reverse proxy) still sees
// the original name while the connection lands on a different port.
//
// Unrelated to proxy.handleForward, which is the plain-HTTP request path.
type ForwardDialer struct {
	name string
	addr string
	d    net.Dialer
}

// NewForward returns a ForwardDialer sending every request to addr
// ("host:port").
func NewForward(name, addr string) *ForwardDialer {
	return &ForwardDialer{name: name, addr: addr}
}

// Dial implements Dialer. hostPort is deliberately ignored.
func (d *ForwardDialer) Dial(ctx context.Context, _ string) (net.Conn, error) {
	return d.d.DialContext(ctx, "tcp", d.addr)
}

// Name implements Dialer.
func (d *ForwardDialer) Name() string { return d.name }
