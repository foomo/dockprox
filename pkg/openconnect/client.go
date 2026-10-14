package openconnect

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/foomo/dockprox/pkg/config"
	"github.com/foomo/dockprox/pkg/secret"
	"github.com/foomo/dockprox/pkg/sshclient"
	"github.com/pkg/errors"
)

const (
	defaultProtocol = "f5"
	// connectTimeout bounds start-up until the SOCKS port answers. It has to
	// cover the user approving an MFA push on their phone.
	connectTimeout = 90 * time.Second
	// stopTimeout is how long openconnect gets to log out after SIGTERM.
	// Kept under menubar.ShutdownTimeout so Stop does not time out on it.
	stopTimeout  = 3 * time.Second
	pollInterval = 200 * time.Millisecond
)

// searchDirs are tried after $PATH: an app launched from Finder gets a
// minimal PATH without the Homebrew prefixes.
var searchDirs = []string{"/opt/homebrew/bin", "/usr/local/bin"}

// Client owns one openconnect process and the SOCKS5 address its ocproxy
// listens on. Like sshclient.Client, the mutex collapses concurrent
// first-use into a single start, which matters here: every start costs the
// user an MFA approval.
type Client struct {
	openconnect string
	ocproxy     string
	args        []string
	username    string
	password    string

	ctx    context.Context //nolint:containedctx // cancelled by Close to abort an in-flight start
	cancel context.CancelFunc

	// held is set by Down and cleared by Up: a VPN the user disconnected
	// stays down until they connect it again, rather than being restarted
	// (and asking for another approval) by the next request.
	held atomic.Bool
	// manualOnly is set by SetAutoconnect(false): Get may still use a
	// running VPN, but only Up starts one.
	manualOnly atomic.Bool
	// abortMu guards abort, which cancels the start in flight, if any.
	abortMu sync.Mutex
	abort   context.CancelFunc

	mu        sync.Mutex
	proc      *process
	lastErr   error
	lastErrAt time.Time

	state    atomic.Int32
	onChange atomic.Pointer[func()]
}

type process struct {
	cmd  *exec.Cmd
	addr string
	done chan struct{} // closed once cmd has exited
	out  *tail
}

// ErrDisconnected is returned by Get while the VPN is held down by Down, or
// when it would have to start with autoconnect off.
var ErrDisconnected = errors.New("openconnect: disconnected")

// New returns a Client for an openconnect upstream. Both binaries are
// resolved here so a missing install fails at startup, not on first use.
func New(u config.Upstream) (*Client, error) {
	oc, err := lookPath("openconnect")
	if err != nil {
		return nil, err
	}

	ocp, err := lookPath("ocproxy")
	if err != nil {
		return nil, err
	}

	protocol := u.Protocol
	if protocol == "" {
		protocol = defaultProtocol
	}

	args := []string{"--protocol", protocol, "--passwd-on-stdin", "--non-inter"}
	if u.ServerCert != "" {
		args = append(args, "--servercert", u.ServerCert)
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &Client{
		openconnect: oc,
		ocproxy:     ocp,
		args:        append(args, u.Server),
		username:    u.Auth.Username,
		password:    u.Auth.Password,
		ctx:         ctx,
		cancel:      cancel,
	}, nil
}

// State returns the last known state of the VPN process. It takes no lock.
func (c *Client) State() sshclient.ConnState {
	return sshclient.ConnState(c.state.Load())
}

// OnStateChange registers fn to be called whenever State changes. fn runs
// synchronously, possibly while Get holds its lock, so it must not call back
// into the Client other than State.
func (c *Client) OnStateChange(fn func()) {
	c.onChange.Store(&fn)
}

// SetAutoconnect sets whether Get starts the VPN on demand (the default).
// With it off, Get only uses a running VPN and starting one needs Up.
func (c *Client) SetAutoconnect(on bool) {
	c.manualOnly.Store(!on)
}

// Get returns the SOCKS5 address of a running VPN, starting openconnect if
// it is not running and autoconnect is on. Starting blocks until the tunnel
// is up, which includes any MFA approval, bounded by connectTimeout.
func (c *Client) Get(ctx context.Context) (string, error) {
	return c.get(ctx, false)
}

// Up clears a Down and brings the VPN up, blocking like Get, whether or not
// autoconnect is on.
func (c *Client) Up(ctx context.Context) error {
	c.held.Store(false)

	_, err := c.get(ctx, true)

	return err
}

// Down stops the VPN, aborting a start in flight, and keeps it down: Get
// returns ErrDisconnected until Up is called. State returns to ConnUnknown.
func (c *Client) Down() {
	c.held.Store(true)

	c.abortMu.Lock()
	if c.abort != nil {
		c.abort()
	}
	c.abortMu.Unlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.proc != nil {
		c.proc.stop()
		c.proc = nil
	}

	c.lastErr = nil
	c.setState(sshclient.ConnUnknown)
}

// Close stops the VPN process and aborts an in-flight start. The Client is
// unusable afterwards. Idempotent.
func (c *Client) Close() error {
	c.cancel()

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.proc != nil {
		c.proc.stop()
		c.proc = nil
	}

	c.setState(sshclient.ConnDisconnected)

	return nil
}

// get is Get; explicit marks a start the user asked for (Up), which
// autoconnect does not gate.
func (c *Client) get(ctx context.Context, explicit bool) (string, error) {
	queued := time.Now()

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.ctx.Err() != nil {
		return "", errors.New("openconnect: closed")
	}

	if p := c.proc; p != nil {
		select {
		case <-p.done:
			c.proc = nil
		default:
			c.setState(sshclient.ConnConnected)
			return p.addr, nil
		}
	}

	if !explicit && c.manualOnly.Load() {
		return "", ErrDisconnected
	}

	// A start that failed while this call waited on mu has already answered
	// it. Retrying would prompt the user for another approval per caller.
	if c.lastErr != nil && c.lastErrAt.After(queued) {
		return "", c.lastErr
	}

	sctx, cancel := context.WithCancel(ctx)
	defer cancel()

	if !c.setAbort(cancel) {
		return "", ErrDisconnected
	}
	defer c.setAbort(nil)

	c.setState(sshclient.ConnConnecting)

	p, err := c.start(sctx)
	if err != nil {
		c.setState(sshclient.ConnDisconnected)

		if sctx.Err() == nil {
			c.lastErr, c.lastErrAt = err, time.Now()
		}

		return "", err
	}

	c.proc = p
	c.lastErr = nil
	c.setState(sshclient.ConnConnected)

	return p.addr, nil
}

// setAbort publishes the cancel func of the start about to run, or clears
// it with nil. It reports false when Down got there first; checking held
// under abortMu means a Down either sees this start's cancel or is seen by
// it, never neither.
func (c *Client) setAbort(cancel context.CancelFunc) bool {
	c.abortMu.Lock()
	defer c.abortMu.Unlock()

	if cancel != nil && c.held.Load() {
		return false
	}

	c.abort = cancel

	return true
}

func (c *Client) start(ctx context.Context) (*process, error) {
	// Resolved per start, not in New: a locked password manager must not
	// fail startup, and its unlock prompt belongs to the connect.
	username, err := secret.Resolve(ctx, c.username)
	if err != nil {
		return nil, errors.Wrap(err, "username")
	}

	password, err := secret.Resolve(ctx, c.password)
	if err != nil {
		return nil, errors.Wrap(err, "password")
	}

	port, err := freePort(ctx)
	if err != nil {
		return nil, err
	}

	// openconnect runs the script through /bin/sh.
	script := "'" + c.ocproxy + "' -D " + strconv.Itoa(port)

	out := &tail{}
	// Bound to c.ctx, not ctx: the VPN outlives the call that started it.
	cmd := exec.CommandContext(c.ctx, c.openconnect, append([]string{"--script-tun", "--script", script, "--user", username}, c.args...)...) //nolint:gosec,contextcheck // binaries from lookPath, args from config; ctx deliberately not inherited
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = stopTimeout
	cmd.Stdin = strings.NewReader(password + "\n")
	cmd.Stdout = out
	cmd.Stderr = out

	if err := cmd.Start(); err != nil {
		return nil, errors.Wrap(err, "start openconnect")
	}

	p := &process{
		cmd:  cmd,
		addr: net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
		done: make(chan struct{}),
		out:  out,
	}

	go func() {
		_ = cmd.Wait()

		c.setState(sshclient.ConnDisconnected)
		close(p.done)
	}()

	deadline := time.NewTimer(connectTimeout)
	defer deadline.Stop()

	tick := time.NewTicker(pollInterval)
	defer tick.Stop()

	for {
		select {
		case <-p.done:
			return nil, errors.Errorf("openconnect exited (%s): %s", cmd.ProcessState, out.String())
		case <-ctx.Done():
			p.stop()
			return nil, errors.Wrap(ctx.Err(), "openconnect")
		case <-c.ctx.Done():
			p.stop()
			return nil, errors.New("openconnect: closed")
		case <-deadline.C:
			p.stop()
			return nil, errors.Errorf("openconnect: tunnel not up after %s: %s", connectTimeout, out.String())
		case <-tick.C:
			conn, err := (&net.Dialer{Timeout: pollInterval}).DialContext(ctx, "tcp", p.addr)
			if err == nil {
				_ = conn.Close()
				return p, nil
			}
		}
	}
}

// stop asks openconnect to log out, then kills it if it does not exit in
// time. It returns once the process has exited.
func (p *process) stop() {
	_ = p.cmd.Process.Signal(syscall.SIGTERM)

	select {
	case <-p.done:
	case <-time.After(stopTimeout):
		_ = p.cmd.Process.Kill()
		<-p.done
	}
}

func (c *Client) setState(s sshclient.ConnState) {
	if sshclient.ConnState(c.state.Swap(int32(s))) == s { //nolint:gosec // acceptable
		return
	}

	if fn := c.onChange.Load(); fn != nil && *fn != nil {
		(*fn)()
	}
}

func lookPath(name string) (string, error) {
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}

	for _, dir := range searchDirs {
		p := filepath.Join(dir, name)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return p, nil
		}
	}

	return "", errors.Errorf("%s not found in $PATH or %v (brew install openconnect ocproxy)", name, searchDirs)
}

// freePort returns a currently unused loopback port for ocproxy.
//
// ponytail: the port is free when checked, not when ocproxy binds it; a
// race loses one start. Fix would be ocproxy reporting its bound port.
func freePort(ctx context.Context) (int, error) {
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return 0, errors.Wrap(err, "pick ocproxy port")
	}
	defer ln.Close()

	return ln.Addr().(*net.TCPAddr).Port, nil //nolint:forcetypeassert // tcp listener
}
