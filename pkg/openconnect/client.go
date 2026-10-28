package openconnect

import (
	"context"
	"fmt"
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

	"github.com/charmbracelet/log"
	"github.com/foomo/dockprox/pkg/config"
	"github.com/foomo/dockprox/pkg/secret"
	"github.com/foomo/dockprox/pkg/sshclient"
	"github.com/pkg/errors"
)

const (
	defaultProtocol = "f5"
	// stopTimeout is how long openconnect gets to log out after SIGTERM.
	// Kept under menubar.ShutdownTimeout so Stop does not time out on it.
	stopTimeout  = 3 * time.Second
	pollInterval = 200 * time.Millisecond
	// vpncScriptPrefix starts the line of `openconnect --version` that names
	// the vpnc-script it would run without --script.
	vpncScriptPrefix = "Default vpnc-script (override with --script): "
)

// connectTimeout bounds start-up until the tunnel is up. It has to cover
// the user approving an MFA push on their phone. A var so tests can shorten
// it.
var connectTimeout = 90 * time.Second

// searchDirs are tried after $PATH: an app launched from Finder gets a
// minimal PATH without the Homebrew prefixes.
var searchDirs = []string{"/opt/homebrew/bin", "/usr/local/bin"}

// Client owns one openconnect process and the SOCKS5 address its ocproxy
// listens on. Like sshclient.Client, the mutex collapses concurrent
// first-use into a single start, which matters here: every start costs the
// user an MFA approval.
//
// With sudo set, the process runs as root with a kernel tun instead
// (config.Upstream.Tun): ocproxy's userspace TCP stack stalls for a minute
// under bulk upload when the VPN has no DTLS (TCP over TCP), the kernel's
// degrades gracefully. There is no SOCKS address then; the kernel routes
// direct dials through the tun. The VPN's DNS stays out of the system
// resolver, as in ocproxy mode: vpnc-script runs without its nameservers,
// which are handed to DNS instead, so VPN-only names resolve in dockprox
// and public names keep resolving publicly.
type Client struct {
	openconnect string
	// ocproxy runs the VPN rootless; tun, sudo, kill and vpncScript run it
	// as root with a kernel tun instead (config.Upstream.Tun).
	ocproxy    string
	tun        bool
	sudo       string
	kill       string
	vpncScript string
	args       []string
	username   string
	password   string

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
	onDrop   atomic.Pointer[func()]
	// resolver resolves through the running tunnel's nameservers in tun
	// mode; see Resolver.
	resolver atomic.Pointer[net.Resolver]
}

type process struct {
	cmd  *exec.Cmd
	addr string        // SOCKS5 address of ocproxy; empty in tun mode
	dir  string        // tun mode: holds the dns file the vpnc-script wrapper writes once the tunnel is configured
	done chan struct{} // closed once cmd has exited
	out  *tail
	// up is set once the tunnel answered, stopping once stop was called:
	// an exit with up and not stopping is a drop.
	up       atomic.Bool
	stopping atomic.Bool
}

// ErrDisconnected is returned by Get while the VPN is held down by Down, or
// when it would have to start with autoconnect off.
var ErrDisconnected = errors.New("openconnect: disconnected")

// New returns a Client for an openconnect upstream. The binaries are
// resolved here so a missing install fails at startup, not on first use;
// so does a missing sudoers line in tun mode.
func New(u config.Upstream) (*Client, error) {
	oc, err := lookPath("openconnect")
	if err != nil {
		return nil, err
	}

	var ocp, sudo, kill, vpnc string

	if u.Tun {
		if sudo, kill, err = sudoBins(); err != nil {
			return nil, err
		}

		if err = checkSudo(context.Background(), sudo, oc, kill); err != nil {
			return nil, err
		}

		if vpnc, err = defaultVpncScript(context.Background(), oc); err != nil {
			return nil, err
		}
	} else if ocp, err = lookPath("ocproxy"); err != nil {
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
		tun:         u.Tun,
		sudo:        sudo,
		kill:        kill,
		vpncScript:  vpnc,
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

// OnDrop registers fn to be called, on its own goroutine, when a VPN that
// was up exits without Down or Close asking it to, e.g. when the server
// ends the session.
func (c *Client) OnDrop(fn func()) {
	c.onDrop.Store(&fn)
}

// SetAutoconnect sets whether Get starts the VPN on demand (the default).
// With it off, Get only uses a running VPN and starting one needs Up.
func (c *Client) SetAutoconnect(on bool) {
	c.manualOnly.Store(!on)
}

// Get returns the SOCKS5 address of a running VPN, starting openconnect if
// it is not running and autoconnect is on. Starting blocks until the tunnel
// is up, which includes any MFA approval, bounded by connectTimeout. In tun
// mode the address is empty: dial direct, the kernel routes through the
// tun.
func (c *Client) Get(ctx context.Context) (string, error) {
	return c.get(ctx, false)
}

// Resolver resolves names through the nameservers the VPN pushed, in tun
// mode while it is up, or returns nil: the system resolver does not know
// VPN names, since vpnc-script runs without them. Pass it to a net.Dialer.
func (c *Client) Resolver() *net.Resolver {
	return c.resolver.Load()
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
		c.stop(c.proc)
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
		c.stop(c.proc)
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

	p, err := c.newProcess(ctx, username)
	if err != nil {
		return nil, err
	}

	cmd, out := p.cmd, p.out
	cmd.Cancel = func() error { return c.signal(p, syscall.SIGTERM) }
	cmd.WaitDelay = stopTimeout
	cmd.Stdin = strings.NewReader(password + "\n")
	cmd.Stdout = out
	cmd.Stderr = out

	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(p.dir)

		return nil, errors.Wrap(err, "start openconnect")
	}

	go func() {
		err := cmd.Wait()
		log.Info("openconnect exited", "state", cmd.ProcessState, "err", err)

		dropped := p.up.Load() && !p.stopping.Load() && c.ctx.Err() == nil

		c.resolver.Store(nil)

		_ = os.RemoveAll(p.dir)

		c.setState(sshclient.ConnDisconnected)
		close(p.done)

		if fn := c.onDrop.Load(); dropped && fn != nil && *fn != nil {
			go (*fn)()
		}
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
			c.stop(p)
			return nil, errors.Wrap(ctx.Err(), "openconnect")
		case <-c.ctx.Done():
			c.stop(p)
			return nil, errors.New("openconnect: closed")
		case <-deadline.C:
			c.stop(p)
			return nil, errors.Errorf("openconnect: tunnel not up after %s: %s", connectTimeout, out.String())
		case <-tick.C:
			if c.ready(ctx, p) {
				p.up.Store(true)

				return p, nil
			}
		}
	}
}

// newProcess builds the openconnect command for this mode; nothing runs
// yet. Bound to c.ctx, not ctx: the VPN outlives the call that started it.
func (c *Client) newProcess(ctx context.Context, username string) (*process, error) {
	p := &process{done: make(chan struct{}), out: &tail{}}

	if c.tun {
		dir, err := os.MkdirTemp("", "dockprox-tun-")
		if err != nil {
			return nil, errors.Wrap(err, "tun state dir")
		}

		// sudo -n never prompts: without the sudoers line it fails at once
		// instead of hanging on a password. It passes stdin through, so the
		// VPN password still arrives on openconnect's stdin.
		p.dir = dir
		p.cmd = exec.CommandContext(c.ctx, c.sudo, append([]string{"-n", c.openconnect, "--script", tunScript(c.vpncScript, filepath.Join(dir, "dns")), "--user", username}, c.args...)...) //nolint:gosec,contextcheck // binaries from lookPath, args from config; ctx deliberately not inherited
		// Own process group: SIGKILL cannot be relayed by sudo, so the kill
		// goes to the group, which holds sudo and openconnect.
		p.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

		return p, nil
	}

	port, err := freePort(ctx)
	if err != nil {
		return nil, err
	}

	// openconnect runs the script through /bin/sh.
	script := "'" + c.ocproxy + "' -D " + strconv.Itoa(port)

	p.addr = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	p.cmd = exec.CommandContext(c.ctx, c.openconnect, append([]string{"--script-tun", "--script", script, "--user", username}, c.args...)...) //nolint:gosec,contextcheck // binaries from lookPath, args from config; ctx deliberately not inherited

	return p, nil
}

// ready reports whether the tunnel is up: ocproxy's SOCKS port answers, or
// in tun mode vpnc-script has configured the tun and left its nameservers
// in p.dir, which become the Resolver.
func (c *Client) ready(ctx context.Context, p *process) bool {
	if c.tun {
		b, err := os.ReadFile(filepath.Join(p.dir, "dns"))
		if err != nil {
			return false
		}

		c.resolver.Store(newResolver(strings.Fields(string(b))))

		return true
	}

	conn, err := (&net.Dialer{Timeout: pollInterval}).DialContext(ctx, "tcp", p.addr)
	if err != nil {
		return false
	}

	_ = conn.Close()

	return true
}

// newResolver resolves through servers, rotating over them, or returns nil
// for none so a net.Dialer falls back to the system resolver.
func newResolver(servers []string) *net.Resolver {
	if len(servers) == 0 {
		return nil
	}

	var n atomic.Uint32

	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			server := servers[int(n.Add(1)-1)%len(servers)]
			if _, _, err := net.SplitHostPort(server); err != nil {
				server = net.JoinHostPort(server, "53")
			}

			return (&net.Dialer{}).DialContext(ctx, network, server)
		},
	}
}

// stop asks openconnect to log out, then kills it if it does not exit in
// time. It returns once the process has exited.
func (c *Client) stop(p *process) {
	p.stopping.Store(true)
	_ = c.signal(p, syscall.SIGTERM)

	select {
	case <-p.done:
	case <-time.After(stopTimeout):
		_ = c.signal(p, syscall.SIGKILL)
		<-p.done
	}
}

// signal delivers sig to openconnect. In tun mode it runs as root under
// sudo, which the unprivileged dockprox cannot signal (EPERM), so the signal
// goes through sudo as well: SIGTERM to sudo, which relays it to openconnect
// (logout and vpnc-script cleanup); SIGKILL to the process group, since sudo
// cannot relay a signal it cannot catch.
func (c *Client) signal(p *process, sig syscall.Signal) error {
	if !c.tun {
		return p.cmd.Process.Signal(sig)
	}

	pid := strconv.Itoa(p.cmd.Process.Pid)

	args := []string{"-n", c.kill, "-TERM", pid}
	if sig == syscall.SIGKILL {
		args = []string{"-n", c.kill, "-KILL", "--", "-" + pid}
	}

	return exec.Command(c.sudo, args...).Run() //nolint:gosec,noctx // binaries from lookPath; a kill must not be cut short by any caller's ctx
}

// tunScript wraps vpncScript for --script: it runs it without the VPN's
// nameservers, so the system resolver is left alone, and once the tunnel is
// configured writes those nameservers to dnsFile for DNS. openconnect runs
// the script through /bin/sh.
func tunScript(vpncScript, dnsFile string) string {
	return "dns=$INTERNAL_IP4_DNS; unset INTERNAL_IP4_DNS INTERNAL_IP6_DNS; " +
		shellQuote(vpncScript) + "; rc=$?; " +
		"[ \"$reason\" = connect ] && printf '%s\\n' \"$dns\" > " + shellQuote(dnsFile) + "; " +
		"exit $rc"
}

// defaultVpncScript asks openconnect which vpnc-script it runs by default,
// which tunScript wraps.
func defaultVpncScript(ctx context.Context, openconnect string) (string, error) {
	out, err := exec.CommandContext(ctx, openconnect, "--version").Output()
	if err != nil {
		return "", errors.Wrap(err, "openconnect --version")
	}

	for line := range strings.Lines(string(out)) {
		if path, ok := strings.CutPrefix(strings.TrimSpace(line), vpncScriptPrefix); ok {
			return path, nil
		}
	}

	return "", errors.New("openconnect --version names no default vpnc-script")
}

// ErrSudoers reports a tun mode start without the sudoers drop-in that
// lets openconnect and kill run as root without a password.
var ErrSudoers = errors.New("tun mode needs passwordless sudo for openconnect and kill")

// sudoBins resolves sudo and kill, which tun mode runs.
func sudoBins() (string, string, error) {
	sudo, err := lookPath("sudo")
	if err != nil {
		return "", "", err
	}

	kill, err := lookPath("kill")
	if err != nil {
		return "", "", err
	}

	return sudo, kill, nil
}

// checkSudo verifies that openconnect and kill may run as root without a
// password, which tun mode needs to start and to stop openconnect.
func checkSudo(ctx context.Context, sudo, openconnect, kill string) error {
	for _, bin := range []string{openconnect, kill} {
		if err := exec.CommandContext(ctx, sudo, "-n", "-l", bin).Run(); err != nil {
			return fmt.Errorf("%w: run \"dockprox tun install\" or use the menu bar's Settings > Install VPN sudoers (writes %s)", ErrSudoers, SudoersPath)
		}
	}

	return nil
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
