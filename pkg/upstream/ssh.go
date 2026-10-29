package upstream

import (
	"context"
	"net"
	"time"

	"github.com/charmbracelet/log"
	"github.com/foomo/dockprox/pkg/config"
	"github.com/foomo/dockprox/pkg/sshclient"
	"github.com/pkg/errors"
)

// SSHDialer opens targets as channels on an SSH connection to a bastion,
// the programmatic equivalent of `ssh -D`. The SSH connection itself is
// established lazily on first Dial.
type SSHDialer struct {
	name string
	cli  *sshclient.Client
	via  Dialer
}

// NewSSH returns an SSHDialer for the given ssh upstream. The target is
// validated here — an unreadable key or a missing agent socket fails at
// startup rather than on first use. A non-nil via carries the TCP
// connection to the bastion, e.g. through a VPN.
func NewSSH(name string, u config.Upstream, via Dialer) (*SSHDialer, error) {
	t, err := sshclient.New(u)
	if err != nil {
		return nil, err
	}

	if err := t.Validate(); err != nil {
		return nil, err
	}

	if via != nil {
		t.Dial = via.Dial
	}

	return &SSHDialer{name: name, cli: sshclient.NewClient(t), via: via}, nil
}

// Name implements Dialer.
func (d *SSHDialer) Name() string { return d.name }

// Dial implements Dialer.
func (d *SSHDialer) Dial(ctx context.Context, hostPort string) (net.Conn, error) {
	start := time.Now()

	if err := d.connectVia(ctx); err != nil {
		return nil, err
	}

	c, err := d.cli.DialContext(ctx, "tcp", hostPort)
	log.Debug("ssh channel", "upstream", d.name, "target", hostPort, "dur", time.Since(start), "err", err)

	return c, err
}

// Connect establishes the SSH connection without opening a target channel,
// so a tunnel can be brought up before it carries any traffic. Dial does
// this implicitly on first use; Connect only makes it explicit and eager.
// Idempotent — a live connection is reused after a keepalive probe.
//
// State() reports ConnConnecting from the moment this is called until the
// attempt settles, so a caller that notifies its observers immediately
// afterwards will publish the in-flight state rather than the stale one.
func (d *SSHDialer) Connect(ctx context.Context) error {
	// The via upstream shows its own progress; marking this connection as
	// connecting before it is up would leave it stuck there if via fails.
	if err := d.connectVia(ctx); err != nil {
		return err
	}

	d.cli.MarkConnecting()

	_, err := d.cli.Get(ctx)

	return err
}

// Up connects, undoing a Down, whether or not autoconnect is on. The user
// asked for this connection, so a via upstream that can be brought up
// explicitly (a VPN) is brought up too.
func (d *SSHDialer) Up(ctx context.Context) error {
	if u, ok := d.via.(interface {
		Up(ctx context.Context) error
	}); ok {
		if err := u.Up(ctx); err != nil {
			return errors.Wrapf(err, "via %s", d.via.Name())
		}
	} else if err := d.connectVia(ctx); err != nil {
		return err
	}

	return d.cli.Up(ctx)
}

// SetAutoconnect sets whether Dial and Connect open the SSH connection on
// demand (the default). With it off, they only reuse a live one.
func (d *SSHDialer) SetAutoconnect(on bool) { d.cli.SetAutoconnect(on) }

// Down closes the SSH connection and keeps it closed until Up: Dial and
// Connect fail with sshclient.ErrDisconnected meanwhile.
func (d *SSHDialer) Down() { d.cli.Down() }

// Close tears down the SSH connection.
func (d *SSHDialer) Close() error { return d.cli.Close() }

// State returns the last known connection state of the underlying SSH
// client. Passive — see sshclient.Client.State.
func (d *SSHDialer) State() sshclient.ConnState { return d.cli.State() }

// OnStateChange registers fn to run whenever State changes. See
// sshclient.Client.OnStateChange for the calling constraints.
func (d *SSHDialer) OnStateChange(fn func()) { d.cli.OnStateChange(fn) }

// connectVia brings up a via upstream that has its own connection, such as
// a VPN, before the SSH dial starts: that can take as long as an MFA
// approval, which the SSH handshake timeout must not cover.
func (d *SSHDialer) connectVia(ctx context.Context) error {
	if c, ok := d.via.(interface {
		Connect(ctx context.Context) error
	}); ok {
		return errors.Wrapf(c.Connect(ctx), "via %s", d.via.Name())
	}

	return nil
}
