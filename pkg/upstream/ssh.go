package upstream

import (
	"context"
	"net"

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
}

// NewSSH returns an SSHDialer for the given ssh upstream. The target is
// validated here — an unreadable key or a missing agent socket fails at
// startup rather than on first use.
func NewSSH(name string, u config.Upstream) (*SSHDialer, error) {
	t, err := sshclient.New(u)
	if err != nil {
		return nil, err
	}

	if err := t.Validate(); err != nil {
		return nil, err
	}

	return &SSHDialer{name: name, cli: sshclient.NewClient(t)}, nil
}

// Name implements Dialer.
func (d *SSHDialer) Name() string { return d.name }

// Dial implements Dialer.
func (d *SSHDialer) Dial(ctx context.Context, hostPort string) (net.Conn, error) {
	cli, err := d.cli.Get(ctx)
	if err != nil {
		return nil, err
	}

	c, err := cli.DialContext(ctx, "tcp", hostPort)
	if err != nil {
		return nil, errors.Wrapf(err, "ssh dial %s", hostPort)
	}

	return c, nil
}

// Close tears down the SSH connection.
func (d *SSHDialer) Close() error { return d.cli.Close() }

// State returns the last known connection state of the underlying SSH
// client. Passive — see sshclient.Client.State.
func (d *SSHDialer) State() sshclient.ConnState { return d.cli.State() }
