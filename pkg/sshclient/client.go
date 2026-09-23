package sshclient

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pkg/errors"
	"golang.org/x/crypto/ssh"
)

// handshakeTimeout bounds the TCP dial and SSH handshake.
const handshakeTimeout = 15 * time.Second

// ConnState is the last known state of a Client's SSH connection, updated
// opportunistically by Get and Close. Every state but ConnConnecting is a
// passive signal: it reflects the outcome of the most recent call, not
// real-time connectivity. ConnConnecting is the exception — it is set while
// a dial is actually in flight.
type ConnState int

const (
	ConnUnknown      ConnState = iota // never attempted
	ConnConnected                     // last Get succeeded (fresh dial or live keepalive)
	ConnDisconnected                  // last Get attempt failed, or Close was called
	// ConnConnecting means a dial is in flight right now. Appended rather
	// than inserted so the existing values keep their numbers, and so
	// ConnUnknown stays the zero value.
	ConnConnecting
)

// String returns the lowercase state name.
func (s ConnState) String() string {
	switch s {
	case ConnConnected:
		return "connected"
	case ConnDisconnected:
		return "disconnected"
	case ConnConnecting:
		return "connecting"
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

	mu  sync.Mutex
	cli *ssh.Client

	// state lives outside mu because State() is called from the menubar UI
	// thread while Get may be holding mu for the length of a handshake —
	// up to handshakeTimeout twice over, and unbounded when authMethods
	// waits on an agent that prompts for confirmation. Reading it under mu
	// would freeze the menu bar for that whole window.
	state atomic.Int32
}

// NewClient returns a Client for t. No network activity happens here.
func NewClient(t *Target) *Client {
	return &Client{target: t}
}

// State returns the last known connection state. Passive — it reflects the
// outcome of the most recent Get call, and does not itself probe the
// network. It takes no lock, so it never blocks behind an in-flight
// handshake.
func (c *Client) State() ConnState {
	return ConnState(c.state.Load())
}

// MarkConnecting publishes ConnConnecting without touching the connection,
// so a caller about to invoke Get can make the pending attempt visible to
// observers before Get blocks — either on the mutex, or on the dial itself.
//
// Get sets the same state once it reaches its own dial, so calling this is
// optional; it only moves the transition earlier.
func (c *Client) MarkConnecting() {
	c.state.Store(int32(ConnConnecting))
}

// Get returns a live *ssh.Client, connecting or reconnecting as needed.
func (c *Client) Get(ctx context.Context) (*ssh.Client, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cli != nil {
		if _, _, err := c.cli.SendRequest("keepalive@openssh.com", true, nil); err == nil {
			c.state.Store(int32(ConnConnected))
			return c.cli, nil
		}

		_ = c.cli.Close()
		c.cli = nil
	}

	// Published before the dial so an observer (the menu bar) can show the
	// attempt while it is in flight — which is the whole window in which
	// an agent may be waiting on the user to confirm.
	c.state.Store(int32(ConnConnecting))

	cli, err := c.dial(ctx)
	if err != nil {
		c.state.Store(int32(ConnDisconnected))
		return nil, err
	}

	c.cli = cli
	c.state.Store(int32(ConnConnected))

	return cli, nil
}

// Close tears down the connection if one is open. Idempotent.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.state.Store(int32(ConnDisconnected))

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

	// One deadline covers the whole attempt, and it is established before
	// the agent is touched. authMethods dials the agent socket, and the
	// signer callback it returns is invoked later, during the handshake —
	// so an agent that accepts the connection and then stalls (a locked or
	// wedged 1Password) would otherwise hang here with no bound at all,
	// holding c.mu and queueing every other connection through this
	// upstream behind it.
	dialCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()

	methods, cleanup, err := c.target.authMethods(dialCtx)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	addr := c.target.Addr()

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
