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

const (
	// handshakeTimeout bounds the TCP dial and SSH handshake.
	handshakeTimeout = 15 * time.Second
	// approvalTimeout replaces handshakeTimeout while the agent holds a sign
	// request, e.g. behind a 1Password prompt that is suppressed because
	// dockprox is never the foreground app. Keep it under sshd's default
	// LoginGraceTime of 120s, after which the server drops the connection.
	approvalTimeout = 60 * time.Second
)

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
	// lastErr is the outcome of the last failed dial and when it failed,
	// so calls that queued on mu behind that dial can share its answer.
	lastErr   error
	lastErrAt time.Time

	// state lives outside mu because State() is called from the menubar UI
	// thread while Get may be holding mu for the length of a handshake —
	// up to approvalTimeout while the agent waits on the user. Reading it
	// under mu would freeze the menu bar for that whole window.
	state    atomic.Int32
	onChange atomic.Pointer[func()]
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
	c.setState(ConnConnecting)
}

// OnStateChange registers fn to be called whenever State changes, including
// mid-dial transitions such as ConnAwaitingApproval. fn runs synchronously
// on the goroutine that changed the state, possibly while Get holds its
// lock, so it must not call back into the Client other than State.
func (c *Client) OnStateChange(fn func()) {
	c.onChange.Store(&fn)
}

// Get returns a live *ssh.Client, connecting or reconnecting as needed.
func (c *Client) Get(ctx context.Context) (*ssh.Client, error) {
	queued := time.Now()

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cli != nil {
		if _, _, err := c.cli.SendRequest("keepalive@openssh.com", true, nil); err == nil {
			c.setState(ConnConnected)
			return c.cli, nil
		}

		_ = c.cli.Close()
		c.cli = nil
	}

	// A dial that failed while this call waited on mu has already answered
	// it. Redialing would stack another full timeout per queued connection.
	if c.lastErr != nil && c.lastErrAt.After(queued) {
		return nil, c.lastErr
	}

	// Published before the dial so an observer (the menu bar) can show the
	// attempt while it is in flight.
	c.setState(ConnConnecting)

	cli, err := c.dial(ctx)
	if err != nil {
		c.setState(ConnDisconnected)

		// A cancelled caller says nothing about the upstream, so its error
		// must not be handed to the calls queued behind it.
		if ctx.Err() == nil {
			c.lastErr, c.lastErrAt = err, time.Now()
		}

		return nil, err
	}

	c.cli = cli
	c.lastErr = nil
	c.setState(ConnConnected)

	return cli, nil
}

// Close tears down the connection if one is open. Idempotent.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.setState(ConnDisconnected)

	if c.cli == nil {
		return nil
	}

	err := c.cli.Close()
	c.cli = nil

	return err
}

func (c *Client) setState(s ConnState) {
	if ConnState(c.state.Swap(int32(s))) == s { //nolint:gosec // acceptable
		return
	}

	if fn := c.onChange.Load(); fn != nil && *fn != nil {
		(*fn)()
	}
}

func (c *Client) dial(ctx context.Context) (*ssh.Client, error) {
	hkc, err := c.target.hostKeyCallback()
	if err != nil {
		return nil, err
	}

	// One deadline covers the agent socket and the SSH connection, and it
	// is established before the agent is touched: the signer callback runs
	// during the handshake, so an agent that accepts the connection and
	// then stalls would otherwise hang here with no bound at all, holding
	// c.mu and queueing every other connection through this upstream.
	att := newAttempt(handshakeTimeout, approvalTimeout)
	att.onSign = func(pending bool) {
		if pending {
			c.setState(ConnAwaitingApproval)
		} else {
			c.setState(ConnConnecting)
		}
	}

	// Past the TCP dial only socket deadlines reach the I/O, so cancelling
	// ctx (StopTunnel, Stop, a dropped client) is turned into an expired one.
	stop := context.AfterFunc(ctx, att.abort)
	defer stop()

	dialCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()

	methods, cleanup, err := c.target.authMethods(dialCtx, att)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	addr := c.target.Addr()

	conn, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", addr)
	if err != nil {
		return nil, errors.Wrapf(err, "dial ssh %s", addr)
	}

	att.add(conn)

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

	// Once abort has started it may expire conn's deadline at any moment,
	// so a handshake that finished just as ctx was cancelled is discarded.
	if !stop() {
		_ = sc.Close()
		return nil, errors.Wrapf(ctx.Err(), "ssh handshake %s", addr)
	}

	_ = conn.SetDeadline(time.Time{})

	return ssh.NewClient(sc, chans, reqs), nil
}
