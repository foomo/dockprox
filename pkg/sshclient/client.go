package sshclient

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/log"
	"github.com/pkg/errors"
	"golang.org/x/crypto/ssh"
)

const (
	// handshakeTimeout bounds the TCP dial and SSH handshake.
	handshakeTimeout = 15 * time.Second
	// viaHandshakeTimeout replaces handshakeTimeout when Target.Dial routes
	// the connection through another upstream. The first connection through
	// a freshly started VPN can take ~20s (observed with F5 + ocproxy).
	viaHandshakeTimeout = 45 * time.Second
	// approvalTimeout replaces handshakeTimeout while the agent holds a sign
	// request, e.g. behind a 1Password prompt that is suppressed because
	// dockprox is never the foreground app. Keep it under sshd's default
	// LoginGraceTime of 120s, after which the server drops the connection.
	approvalTimeout = 60 * time.Second
)

// timing holds the watcher's knobs. A struct on the Client rather than
// package variables, so a test sets its own without racing another's
// watcher.
type timing struct {
	// heartbeat is how often an open connection that has read nothing is
	// probed. A new login can take ~30s on some servers, so the connection
	// is kept from idling out (VPN, NAT, sshd) and a dead one is found
	// before a request has to wait on it.
	heartbeat time.Duration
	// probe bounds a probe. A conn whose path died silently (sleep/wake,
	// VPN drop) never answers.
	probe time.Duration
	// stall replaces probe while a write to the connection is blocked. The
	// path is then congested rather than idle, and a probe can only answer
	// once it drains: TCP inside a TCP-only VPN (ocproxy over TLS) freezes
	// for about a minute at a time under bulk uploads. Closing it then
	// would kill every channel it carries and still wait for the same path.
	stall time.Duration
}

var defaultTiming = timing{heartbeat: 15 * time.Second, probe: 5 * time.Second, stall: 2 * time.Minute}

// Client holds a lazily-established SSH connection to a Target. The first
// use performs the handshake; later calls reuse the connection as long as
// it is open, and reconnect once it is not.
//
// Liveness is the watcher's job: each open connection has a goroutine that
// keeps it alive, closes it when a probe fails and forgets it once it
// closes, by either side. It never dials. There is no backoff state: the
// mutex collapses concurrent first-use into a single handshake, and the
// same path serves reconnection after a sleep/wake.
type Client struct {
	target *Target
	timing timing

	mu  sync.Mutex
	cli *ssh.Client
	// conn is the transport under cli; it records when cli last read.
	conn *readConn
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

	// held is set by Down and cleared by Up: a connection the user closed
	// stays closed until they open it again, rather than being redialled
	// (and asking the agent for another approval) by the next request.
	held atomic.Bool
	// manualOnly is set by SetAutoconnect(false): Get may still reuse a
	// live connection, but only Up dials a new one.
	manualOnly atomic.Bool
	// abortMu guards abort, which cancels the dial in flight, if any.
	abortMu sync.Mutex
	abort   context.CancelFunc
}

// ErrDisconnected is returned by Get while the connection is held down by
// Down, or when it would have to dial with autoconnect off.
var ErrDisconnected = errors.New("ssh: disconnected")

// NewClient returns a Client for t. No network activity happens here.
func NewClient(t *Target) *Client {
	return &Client{target: t, timing: defaultTiming}
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
//
// It does nothing while the connection is held down by Down or autoconnect
// is off, since Get will not dial then.
func (c *Client) MarkConnecting() {
	if c.held.Load() || c.manualOnly.Load() {
		return
	}

	c.setState(ConnConnecting)
}

// OnStateChange registers fn to be called whenever State changes, including
// mid-dial transitions such as ConnAwaitingApproval. fn runs synchronously
// on the goroutine that changed the state, possibly while Get holds its
// lock, so it must not call back into the Client other than State.
func (c *Client) OnStateChange(fn func()) {
	c.onChange.Store(&fn)
}

// SetAutoconnect sets whether Get dials on demand (the default). With it
// off, Get only reuses a live connection and a new one needs Up.
func (c *Client) SetAutoconnect(on bool) {
	c.manualOnly.Store(!on)
}

// Get returns the open *ssh.Client, connecting or reconnecting as needed
// unless autoconnect is off. The connection is not probed: a dead one is
// closed by its watcher within a heartbeat, or found by the first channel
// open on it — see DialContext.
func (c *Client) Get(ctx context.Context) (*ssh.Client, error) {
	return c.get(ctx, false)
}

// DialContext opens addr as a channel on the connection, like
// ssh.Client.DialContext, connecting as needed. A channel open that fails
// for any reason other than the server refusing it means the connection
// is gone (a path that died silently is noticed here before the watcher
// probes it), so it is closed and the open is retried once on a new one.
func (c *Client) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	for retry := false; ; retry = true {
		cli, err := c.get(ctx, false)
		if err != nil {
			return nil, err
		}

		conn, err := cli.DialContext(ctx, network, addr)
		if err == nil {
			return conn, nil
		}

		if errors.As(err, new(*ssh.OpenChannelError)) || retry || ctx.Err() != nil {
			return nil, errors.Wrapf(err, "ssh dial %s", addr)
		}

		log.Debug("ssh channel open failed, redialing", "addr", c.target.Addr(), "err", err)
		c.forget(cli)
	}
}

// Up clears a Down and connects, blocking like Get, whether or not
// autoconnect is on.
func (c *Client) Up(ctx context.Context) error {
	c.held.Store(false)

	_, err := c.get(ctx, true)

	return err
}

// Down closes the connection, aborting a dial in flight, and keeps it
// closed: Get returns ErrDisconnected until Up is called. State returns to
// ConnUnknown.
func (c *Client) Down() {
	c.held.Store(true)

	c.abortMu.Lock()
	if c.abort != nil {
		c.abort()
	}
	c.abortMu.Unlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cli != nil {
		_ = c.cli.Close()
		c.cli = nil
	}

	c.lastErr = nil
	c.setState(ConnUnknown)
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

// forget closes cli and, if it is still the current connection, drops it
// so the next get dials.
func (c *Client) forget(cli *ssh.Client) {
	_ = cli.Close()

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cli == cli {
		c.cli = nil
		c.setState(ConnDisconnected)
	}
}

// get is Get; explicit marks a dial the user asked for (Up), which
// autoconnect does not gate.
func (c *Client) get(ctx context.Context, explicit bool) (*ssh.Client, error) {
	queued := time.Now()

	c.mu.Lock()
	defer c.mu.Unlock()

	addr := c.target.Addr()
	log.Debug("ssh get", "addr", addr, "wait", time.Since(queued))

	if c.held.Load() {
		return nil, ErrDisconnected
	}

	if c.cli != nil {
		c.setState(ConnConnected)
		return c.cli, nil
	}

	if !explicit && c.manualOnly.Load() {
		return nil, ErrDisconnected
	}

	// A dial that failed while this call waited on mu has already answered
	// it. Redialing would stack another full timeout per queued connection.
	if c.lastErr != nil && c.lastErrAt.After(queued) {
		return nil, c.lastErr
	}

	dctx, cancel := context.WithCancel(ctx)
	defer cancel()

	if !c.setAbort(cancel) {
		return nil, ErrDisconnected
	}
	defer c.setAbort(nil)

	// Published before the dial so an observer (the menu bar) can show the
	// attempt while it is in flight.
	c.setState(ConnConnecting)

	start := time.Now()
	cli, conn, err := c.dial(dctx)
	log.Debug("ssh dial done", "addr", addr, "dur", time.Since(start), "err", err)

	if err != nil {
		c.setState(ConnDisconnected)

		// A cancelled caller (or a Down) says nothing about the upstream,
		// so its error must not be handed to the calls queued behind it.
		if dctx.Err() == nil {
			c.lastErr, c.lastErrAt = err, time.Now()
		}

		return nil, err
	}

	c.cli, c.conn = cli, conn
	c.lastErr = nil
	c.setState(ConnConnected)

	go c.watch(cli, conn)

	return cli, nil
}

// watch keeps cli alive until it closes, probing it whenever it has read
// nothing for timing.heartbeat and closing it when a probe fails. Once cli
// is closed, by either side or for any reason, it is logged and forgotten
// so the next Get dials.
func (c *Client) watch(cli *ssh.Client, conn *readConn) {
	addr := c.target.Addr()
	closed := make(chan struct{})

	go func() {
		defer close(closed)

		err := cli.Wait()
		log.Info("ssh closed", "addr", addr, "err", err)

		c.mu.Lock()
		defer c.mu.Unlock()

		if c.cli == cli {
			c.cli = nil
			c.setState(ConnDisconnected)
		}
	}()

	t := time.NewTicker(c.timing.heartbeat)
	defer t.Stop()

	for {
		select {
		case <-closed:
			return
		case <-t.C:
		}

		if time.Since(conn.lastRead()) < c.timing.heartbeat {
			continue
		}

		if err := c.keepalive(cli, conn); err != nil {
			log.Warn("ssh heartbeat", "addr", addr, "err", err)

			_ = cli.Close()

			return
		}
	}
}

// keepalive probes cli, failing after timing.probe, or up to timing.stall
// while a write to conn stays blocked.
// The caller closes cli on failure, which also unblocks the probe left
// running.
func (c *Client) keepalive(cli *ssh.Client, conn *readConn) error {
	done := make(chan error, 1)

	go func() {
		_, _, err := cli.SendRequest("keepalive@openssh.com", true, nil)
		done <- err
	}()

	stallDeadline := time.Now().Add(c.timing.stall)

	t := time.NewTimer(c.timing.probe)
	defer t.Stop()

	for {
		select {
		case err := <-done:
			return err
		case <-t.C:
		}

		if conn.writing.Load() == 0 {
			return errors.New("keepalive timeout")
		}

		if !time.Now().Before(stallDeadline) {
			return errors.New("keepalive timeout: connection stalled")
		}

		t.Reset(time.Second)
	}
}

// setAbort publishes the cancel func of the dial about to run, or clears it
// with nil. It reports false when Down got there first; checking held under
// abortMu means a Down either sees this dial's cancel or is seen by it.
func (c *Client) setAbort(cancel context.CancelFunc) bool {
	c.abortMu.Lock()
	defer c.abortMu.Unlock()

	if cancel != nil && c.held.Load() {
		return false
	}

	c.abort = cancel

	return true
}

func (c *Client) setState(s ConnState) {
	if ConnState(c.state.Swap(int32(s))) == s { //nolint:gosec // acceptable
		return
	}

	if fn := c.onChange.Load(); fn != nil && *fn != nil {
		(*fn)()
	}
}

func (c *Client) dial(ctx context.Context) (*ssh.Client, *readConn, error) {
	hkc, err := c.target.hostKeyCallback()
	if err != nil {
		return nil, nil, err
	}

	// One deadline covers the agent socket and the SSH connection, and it
	// is established before the agent is touched: the signer callback runs
	// during the handshake, so an agent that accepts the connection and
	// then stalls would otherwise hang here with no bound at all, holding
	// c.mu and queueing every other connection through this upstream.
	timeout := handshakeTimeout
	if c.target.Dial != nil {
		timeout = viaHandshakeTimeout
	}

	att := newAttempt(timeout, approvalTimeout)
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

	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	methods, cleanup, err := c.target.authMethods(dialCtx, att)
	if err != nil {
		return nil, nil, err
	}
	defer cleanup()

	addr := c.target.Addr()

	var conn net.Conn
	if c.target.Dial != nil {
		conn, err = c.target.Dial(dialCtx, addr)
	} else {
		conn, err = (&net.Dialer{}).DialContext(dialCtx, "tcp", addr)
	}

	if err != nil {
		return nil, nil, errors.Wrapf(err, "dial ssh %s", addr)
	}

	att.add(conn)

	rc := &readConn{Conn: conn}
	rc.touch()

	sc, chans, reqs, err := ssh.NewClientConn(rc, addr, &ssh.ClientConfig{
		User:            c.target.User,
		Auth:            methods,
		HostKeyCallback: hkc,
		Timeout:         timeout,
	})
	if err != nil {
		_ = conn.Close()
		return nil, nil, errors.Wrapf(err, "ssh handshake %s", addr)
	}

	// Once abort has started it may expire conn's deadline at any moment,
	// so a handshake that finished just as ctx was cancelled is discarded.
	if !stop() {
		_ = sc.Close()
		return nil, nil, errors.Wrapf(ctx.Err(), "ssh handshake %s", addr)
	}

	_ = conn.SetDeadline(time.Time{})

	return ssh.NewClient(sc, chans, reqs), rc, nil
}

// readConn records the time of its last successful Read, and how many
// Writes are in flight. A failed Read clears the time, so a connection the
// peer closed is probed, not reused.
type readConn struct {
	net.Conn

	last    atomic.Int64
	writing atomic.Int32
}

func (r *readConn) Write(p []byte) (int, error) {
	r.writing.Add(1)
	defer r.writing.Add(-1)

	return r.Conn.Write(p)
}

func (r *readConn) Read(p []byte) (int, error) {
	n, err := r.Conn.Read(p)
	switch {
	case err != nil:
		r.last.Store(0)
	case n > 0:
		r.touch()
	}

	return n, err
}

func (r *readConn) touch() { r.last.Store(time.Now().UnixNano()) }

func (r *readConn) lastRead() time.Time { return time.Unix(0, r.last.Load()) }
