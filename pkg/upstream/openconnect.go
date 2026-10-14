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
// Dial; hostnames are resolved by the VPN's DNS.
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

// Close stops the VPN.
func (d *OpenConnectDialer) Close() error { return d.cli.Close() }

// State returns the last known state of the VPN process.
func (d *OpenConnectDialer) State() sshclient.ConnState { return d.cli.State() }

// OnStateChange registers fn to run whenever State changes.
func (d *OpenConnectDialer) OnStateChange(fn func()) { d.cli.OnStateChange(fn) }
