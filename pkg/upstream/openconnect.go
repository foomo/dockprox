package upstream

import (
	"context"
	"net"

	"github.com/foomo/dockprox/pkg/config"
	"github.com/foomo/dockprox/pkg/openconnect"
	"github.com/foomo/dockprox/pkg/sshclient"
)

// OpenConnectDialer dials targets through a rootless openconnect VPN: the
// SOCKS5 port of the ocproxy it runs. The VPN is started lazily on first
// Dial; hostnames are resolved by the VPN's DNS. In tun mode (Upstream.Tun)
// the VPN is a kernel tun instead and targets are dialed direct, resolved
// with the VPN's nameservers: the kernel routes them, the system resolver
// stays untouched.
type OpenConnectDialer struct {
	name string
	cli  *openconnect.Client
}

// NewOpenConnect returns an OpenConnectDialer for the given openconnect
// upstream. It fails when openconnect or ocproxy is not installed.
func NewOpenConnect(name string, u config.Upstream) (*OpenConnectDialer, error) {
	cli, err := openconnect.New(u)
	if err != nil {
		return nil, err
	}

	return &OpenConnectDialer{name: name, cli: cli}, nil
}

// Name implements Dialer.
func (d *OpenConnectDialer) Name() string { return d.name }

// Dial implements Dialer.
func (d *OpenConnectDialer) Dial(ctx context.Context, hostPort string) (net.Conn, error) {
	addr, err := d.cli.Get(ctx)
	if err != nil {
		return nil, err
	}

	// Tun mode: the kernel routes the dial, the VPN's nameservers resolve
	// it (see openconnect.Client.Resolver).
	if addr == "" {
		return (&net.Dialer{Resolver: d.cli.Resolver()}).DialContext(ctx, "tcp", hostPort)
	}

	return NewSocks5(d.name, config.Upstream{Addr: addr, DNS: "remote"}).Dial(ctx, hostPort)
}

// Connect brings the VPN up without dialing a target. Idempotent.
func (d *OpenConnectDialer) Connect(ctx context.Context) error {
	_, err := d.cli.Get(ctx)
	return err
}

// Up connects the VPN on demand, undoing a Down. Blocks like Connect.
func (d *OpenConnectDialer) Up(ctx context.Context) error { return d.cli.Up(ctx) }

// Down disconnects the VPN and keeps it down until Up: Dial and Connect
// fail with openconnect.ErrDisconnected meanwhile.
func (d *OpenConnectDialer) Down() { d.cli.Down() }

// SetAutoconnect sets whether Dial and Connect start the VPN on demand (the
// default). With it off, they only use a running one.
func (d *OpenConnectDialer) SetAutoconnect(on bool) { d.cli.SetAutoconnect(on) }

// Close stops the VPN.
func (d *OpenConnectDialer) Close() error { return d.cli.Close() }

// State returns the last known state of the VPN process.
func (d *OpenConnectDialer) State() sshclient.ConnState { return d.cli.State() }

// OnStateChange registers fn to run whenever State changes.
func (d *OpenConnectDialer) OnStateChange(fn func()) { d.cli.OnStateChange(fn) }

// OnDrop registers fn to run when the VPN exits on its own. See
// openconnect.Client.OnDrop.
func (d *OpenConnectDialer) OnDrop(fn func()) { d.cli.OnDrop(fn) }
