package sshclient

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/pkg/errors"
	"golang.org/x/crypto/ssh"
)

// handshakeTimeout bounds the TCP dial and SSH handshake.
const handshakeTimeout = 15 * time.Second

// ConnState is the last known state of a Client's SSH connection, updated
// opportunistically by Get and Close. It is a passive signal, not a live
// probe: it reflects the outcome of the most recent call, not real-time
// connectivity.
type ConnState int

const (
	ConnUnknown      ConnState = iota // never attempted
	ConnConnected                     // last Get succeeded (fresh dial or live keepalive)
	ConnDisconnected                  // last Get attempt failed, or Close was called
)

// String returns the lowercase state name.
func (s ConnState) String() string {
	switch s {
	case ConnConnected:
		return "connected"
	case ConnDisconnected:
		return "disconnected"
	default:
		return "unknown"
	}
}

// Client holds a lazily-established SSH connection to a Target. The first
// Dial through it performs the handshake; subsequent calls reuse the
// connection after a keepalive probe, and reconnect when the probe fails.
//
// There is no supervisor goroutine and no backoff state: the mutex
// collapses concurrent first-use into a single handshake, and the same
// path serves reconnection after a sleep/wake.
type Client struct {
	target *Target

	mu    sync.Mutex
	cli   *ssh.Client
	state ConnState
}

// NewClient returns a Client for t. No network activity happens here.
func NewClient(t *Target) *Client {
	return &Client{target: t}
}

// State returns the last known connection state. Passive — it reflects the
// outcome of the most recent Get call, and does not itself probe the
// network.
func (c *Client) State() ConnState {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.state
}

// Get returns a live *ssh.Client, connecting or reconnecting as needed.
func (c *Client) Get(ctx context.Context) (*ssh.Client, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cli != nil {
		if _, _, err := c.cli.SendRequest("keepalive@openssh.com", true, nil); err == nil {
			c.state = ConnConnected
			return c.cli, nil
		}

		_ = c.cli.Close()
		c.cli = nil
	}

	cli, err := c.dial(ctx)
	if err != nil {
		c.state = ConnDisconnected
		return nil, err
	}

	c.cli = cli
	c.state = ConnConnected

	return cli, nil
}

// Close tears down the connection if one is open. Idempotent.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.state = ConnDisconnected

	if c.cli == nil {
		return nil
	}

	err := c.cli.Close()
	c.cli = nil

	return err
}

func (c *Client) dial(ctx context.Context) (*ssh.Client, error) {
	hkc, err := c.target.hostKeyCallback()
	if err != nil {
		return nil, err
	}

	methods, cleanup, err := c.target.authMethods(ctx)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	addr := c.target.Addr()

	dialCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()

	conn, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", addr)
	if err != nil {
		return nil, errors.Wrapf(err, "dial ssh %s", addr)
	}

	if dl, ok := dialCtx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}

	sc, chans, reqs, err := ssh.NewClientConn(conn, addr, &ssh.ClientConfig{
		User:            c.target.User,
		Auth:            methods,
		HostKeyCallback: hkc,
		Timeout:         handshakeTimeout,
	})
	if err != nil {
		_ = conn.Close()
		return nil, errors.Wrapf(err, "ssh handshake %s", addr)
	}

	_ = conn.SetDeadline(time.Time{})

	return ssh.NewClient(sc, chans, reqs), nil
}
