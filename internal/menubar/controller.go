package menubar

import (
	"context"
	"maps"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/log"
	"github.com/foomo/dockprox/pkg/config"
	"github.com/foomo/dockprox/pkg/listener"
	"github.com/foomo/dockprox/pkg/match"
	"github.com/foomo/dockprox/pkg/openconnect"
	"github.com/foomo/dockprox/pkg/proxy"
	"github.com/foomo/dockprox/pkg/sshclient"
	"github.com/foomo/dockprox/pkg/upstream"
	"github.com/pkg/errors"
)

// ShutdownTimeout bounds how long Stop and StopTunnel wait for a
// listener's Serve goroutine to return after its context is cancelled.
//
// Cancelling closes the listener, so Accept unblocks and Serve returns in
// microseconds; this deadline only matters if that goroutine is wedged.
// Exceeding it is not fatal — the listener is already closed and the
// controller state is already updated, so we stop waiting and let the
// goroutine finish on its own rather than blocking the caller forever.
const ShutdownTimeout = 5 * time.Second

// errShutdownTimeout is returned by Stop/StopTunnel when a Serve goroutine
// did not return within ShutdownTimeout.
var errShutdownTimeout = errors.Errorf("shutdown timed out after %s", ShutdownTimeout)

// waitDone waits for every channel in dones to close, sharing one deadline
// across all of them. Returns false if the deadline expired first.
func waitDone(dones []chan struct{}, timeout time.Duration) bool {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	for _, d := range dones {
		if d == nil {
			continue
		}

		select {
		case <-d:
		case <-deadline.C:
			return false
		}
	}

	return true
}

// State is the proxy lifecycle state.
type State int

// Proxy lifecycle states. StateStopped is the zero value.
const (
	StateStopped State = iota
	StateStarting
	StateRunning
	StateError
)

// String returns the lowercase state name.
func (s State) String() string {
	switch s {
	case StateStopped:
		return "stopped"
	case StateStarting:
		return "starting"
	case StateRunning:
		return "running"
	case StateError:
		return "error"
	default:
		return "unknown"
	}
}

// TunnelState is a tunnel's SOCKS5 listener lifecycle, independent of the
// HTTP proxy and other tunnels.
type TunnelState int

// Tunnel listener states. TunnelListening is the zero value.
const (
	TunnelListening TunnelState = iota
	TunnelStopped
)

// String returns the lowercase state name.
func (s TunnelState) String() string {
	switch s {
	case TunnelListening:
		return "listening"
	case TunnelStopped:
		return "stopped"
	default:
		return "unknown"
	}
}

// Status is a snapshot of controller state.
type Status struct {
	State      State
	ListenAddr string
	ConfigPath string
	LastError  error
	Tunnels    []TunnelStatus
	Forwards   []ForwardStatus
	Upstreams  []UpstreamStatus
	// Chrome is the loaded config's chrome section, or nil when the
	// controller is not running or the section is absent.
	Chrome *config.Chrome
}

// ForwardStatus describes one "forward" upstream's fixed dial target. It
// carries no reachability verdict: a forward has no lifecycle the
// controller owns (unlike a tunnel, which owns a listener), so whether the
// endpoint answers is probed on demand by the UI — see ProbeForwards.
type ForwardStatus struct {
	Name string
	// Addr is the upstream's configured fixed dial target ("host:port").
	Addr string
}

// UpstreamStatus describes one upstream that holds a connection the user
// can open and close from the menu: an ssh session or an openconnect VPN.
type UpstreamStatus struct {
	Name string
	// Type is the config type, config.UpstreamSSH or
	// config.UpstreamOpenConnect.
	Type string
	// ConnState is the connection state. ConnUnknown means not connected:
	// never dialled, or disconnected from the menu.
	ConnState sshclient.ConnState
}

// TunnelStatus describes one SOCKS5 listener. The connection of the
// upstream it may be pinned to is reported in Status.Upstreams.
type TunnelStatus struct {
	// Name is the configured listen address, which keys the tunnel.
	Name string
	// Upstream is the pinned upstream, or "" when the listener routes by
	// rules.
	Upstream string
	// Target is a tcp listener's forward target, "" for socks5.
	Target string
	// Addr is the listener's bound address, or "" when State is
	// TunnelStopped.
	Addr string
	// State reflects whether this tunnel's SOCKS5 listener is currently
	// accepting connections.
	State TunnelState
}

// tunnelHandle is the controller's live bookkeeping for one tunnel,
// independent of the HTTP proxy and other tunnels. srv/ctx/cancel/done are
// nil when the tunnel is stopped.
type tunnelHandle struct {
	// name is the configured listen address, which keys the tunnel.
	name string
	// upstream and dialer are the pinned upstream, or "" and nil for a
	// listener that routes by rules.
	upstream string
	dialer   upstream.Dialer
	// listener, reg, matcher and logger rebind the listener on StartTunnel.
	listener config.Listener
	reg      *upstream.Registry
	matcher  *match.Matcher
	logger   *log.Logger

	srv    listener.Server
	ctx    context.Context //nolint:containedctx // supervisor handle: paired with cancel for later teardown, never threaded through calls
	cancel context.CancelFunc
	done   chan struct{}
}

// ProxyController owns the embedded proxy lifecycle.
type ProxyController struct {
	logger  *log.Logger
	cfgPath string

	mu        sync.Mutex
	state     State
	listen    string
	tunnels   map[string]*tunnelHandle
	baseCtx   context.Context //nolint:containedctx // supervisor handle: paired with cancel for later teardown, never threaded through calls
	reg       *upstream.Registry
	cfg       *config.Config
	cancel    context.CancelFunc
	done      chan struct{}
	lastErr   error
	listeners []func(Status)
	// onApproval is set once, before Start; see OnApproval.
	onApproval func(pending bool)
	// manualOnly is the inverse of the autoconnect setting, so the zero
	// value keeps connecting on demand; see SetAutoconnect.
	manualOnly atomic.Bool
}

// New returns a stopped controller bound to cfgPath.
func New(cfgPath string, logger *log.Logger) *ProxyController {
	return &ProxyController{
		logger:  logger,
		cfgPath: cfgPath,
		state:   StateStopped,
	}
}

// Snapshot returns the current status. Safe to call concurrently.
func (c *ProxyController) Snapshot() Status {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.statusLocked()
}

// Subscribe registers a listener for state changes. The returned function
// removes the listener.
func (c *ProxyController) Subscribe(fn func(Status)) func() {
	c.mu.Lock()
	c.listeners = append(c.listeners, fn)
	idx := len(c.listeners) - 1
	c.mu.Unlock()

	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()

		if idx < len(c.listeners) {
			c.listeners[idx] = nil
		}
	}
}

// OnApproval registers fn to be told when an SSH tunnel starts (true) and
// stops (false) waiting on its agent to approve a key. fn runs synchronously
// on the dialing goroutine before the sign request reaches the agent, so it
// can act before the agent decides whether to show its prompt; it must not
// call back into the controller. Call before Start.
func (c *ProxyController) OnApproval(fn func(pending bool)) {
	c.onApproval = fn
}

// Start loads config and runs the proxy in a goroutine. Returns nil if
// already running.
func (c *ProxyController) Start() error {
	c.mu.Lock()
	if c.state == StateRunning || c.state == StateStarting {
		c.mu.Unlock()
		return nil
	}

	c.state = StateStarting
	c.lastErr = nil
	c.mu.Unlock()
	c.notify()

	cfg, err := config.LoadFile(c.cfgPath)
	if err != nil {
		c.fail(errors.Wrap(err, "load config"))
		return err
	}

	c.logger.SetLevel(config.LevelFromString(cfg.LogLevel))

	reg, err := upstream.NewRegistry(cfg)
	if err != nil {
		c.fail(errors.Wrap(err, "registry"))
		return err
	}

	known := map[string]struct{}{}
	for n := range cfg.Upstreams {
		known[n] = struct{}{}
	}

	rules := make([]match.Rule, 0, len(cfg.Rules))
	for _, r := range cfg.Rules {
		rules = append(rules, match.Rule{Pattern: r.Match, Upstream: r.Upstream})
	}

	m, err := match.New(rules, known)
	if err != nil {
		c.fail(errors.Wrap(err, "matcher"))
		return err
	}

	baseCtx, cancel := context.WithCancel(context.Background())

	srv, err := proxy.NewServer(baseCtx, proxy.Options{
		Listen:   cfg.Listen,
		Matcher:  m,
		Registry: reg,
		Logger:   c.logger,
	})
	if err != nil {
		cancel()
		c.fail(errors.Wrap(err, "server"))

		return err
	}

	tunnels, err := buildTunnels(baseCtx, cfg, reg, m, c.logger)
	if err != nil {
		cancel()
		c.fail(errors.Wrap(err, "tunnels"))

		return err
	}

	// Repaint on every connection state change, not only on menu clicks:
	// connections also open lazily from proxied requests, and the
	// approval-pending state appears in the middle of an SSH dial.
	for _, name := range connNames(cfg) {
		switch d := c.connDialer(reg, name).(type) {
		case *upstream.SSHDialer:
			d.OnStateChange(c.sshStateChanged(d.State))
		case *upstream.OpenConnectDialer:
			d.OnStateChange(c.notify)
			d.OnDrop(c.upstreamDropped(reg, name))
		}
	}

	done := make(chan struct{})

	c.mu.Lock()
	c.cancel = cancel
	c.done = done
	c.listen = srv.Addr()
	c.tunnels = tunnels
	c.baseCtx = baseCtx
	c.reg = reg
	c.cfg = cfg
	c.state = StateRunning
	c.mu.Unlock()
	c.notify()

	c.logger.Info("start", "listen", srv.Addr(), "upstreams", len(cfg.Upstreams),
		"rules", len(cfg.Rules), "tunnels", len(tunnels))

	for _, h := range tunnels {
		c.serveTunnel(h, h.ctx)
	}

	c.applyAutoconnect(reg, cfg)

	go func() {
		defer close(done)
		// Every exit path cancels the tunnels too, so nothing dials through
		// the registry anymore: drop SSH connections and stop VPN processes.
		defer func() { _ = reg.Close() }()
		defer func() {
			if r := recover(); r != nil {
				c.fail(errors.Errorf("proxy panic: %v", r))
			}
		}()

		serveErr := srv.Serve(baseCtx)
		unexpected := serveErr != nil && baseCtx.Err() == nil

		// An unexpected exit (not caused by our own cancel, e.g. Stop())
		// takes every tunnel down with it, matching the pre-refactor
		// all-or-nothing behavior for genuine failures. A deliberate
		// StopTunnel on an individual tunnel never reaches this path.
		if unexpected {
			cancel()
		}

		c.mu.Lock()
		if c.state == StateRunning {
			if unexpected {
				c.state = StateError
				c.lastErr = serveErr
			} else {
				c.state = StateStopped
			}

			c.listen = ""
		}
		c.mu.Unlock()
		c.notify()
	}()

	return nil
}

// buildTunnels binds every configured listener (SOCKS5 or TCP forward),
// keyed by its configured listen address. A pinned listener is bound to its
// upstream's dialer; an unpinned one routes by m.
func buildTunnels(
	ctx context.Context,
	cfg *config.Config,
	reg *upstream.Registry,
	m *match.Matcher,
	logger *log.Logger,
) (map[string]*tunnelHandle, error) {
	tunnels := make(map[string]*tunnelHandle)

	for _, l := range cfg.Listeners {
		tctx, tcancel := context.WithCancel(ctx)

		srv, err := listener.New(tctx, l, reg, m, logger)
		if err != nil {
			tcancel()

			for _, h := range tunnels {
				_ = h.srv.Close()
			}

			return nil, errors.Wrapf(err, "listener %q", l.Listen)
		}

		d, _ := reg.Get(l.Upstream)

		tunnels[l.Listen] = &tunnelHandle{
			name:     l.Listen,
			upstream: l.Upstream,
			dialer:   d,
			listener: l,
			reg:      reg,
			matcher:  m,
			logger:   logger,
			srv:      srv,
			ctx:      tctx,
			cancel:   tcancel,
			done:     make(chan struct{}),
		}
	}

	return tunnels, nil
}

// StopTunnel stops the named tunnel's SOCKS5 listener only. The HTTP proxy
// and other tunnels are unaffected. Idempotent if the tunnel is already
// stopped.
func (c *ProxyController) StopTunnel(name string) error {
	c.mu.Lock()

	h, ok := c.tunnels[name]
	if !ok {
		c.mu.Unlock()
		return errors.Errorf("tunnel %q: unknown", name)
	}

	cancel := h.cancel
	done := h.done
	c.mu.Unlock()

	if cancel == nil {
		return nil
	}

	cancel()

	if !waitDone([]chan struct{}{done}, ShutdownTimeout) {
		c.logger.Warn("stop tunnel", "name", name, "err", errShutdownTimeout)

		return errors.Wrapf(errShutdownTimeout, "tunnel %q", name)
	}

	return nil
}

// StartTunnel rebinds the named tunnel's SOCKS5 listener, reusing the same
// upstream's dialer (and its underlying SSH connection state) from the
// running registry, and brings up its pinned upstream chain (an ssh session
// and its via VPN, or a VPN) in the background, even with autoconnect
// off. Idempotent if already running.
//
// The listener binds synchronously — that is fast and its failure is what
// callers need reported — while the upstream is brought up in a
// goroutine, because that dial can block for a TCP connect plus handshake
// and, with an agent that asks for confirmation, for as long as it takes
// the user to approve. StartTunnel therefore returns as soon as the tunnel
// is accepting connections; a failed dial is logged and leaves the tunnel
// listening, with connections falling back to the lazy dial they have
// always used.
func (c *ProxyController) StartTunnel(name string) error {
	c.mu.Lock()

	if c.state != StateRunning {
		c.mu.Unlock()
		return errors.New("controller not running")
	}

	h, ok := c.tunnels[name]
	if !ok {
		c.mu.Unlock()
		return errors.Errorf("tunnel %q: unknown", name)
	}

	if h.srv != nil {
		c.mu.Unlock()
		return nil
	}

	baseCtx := c.baseCtx
	c.mu.Unlock()

	tctx, tcancel := context.WithCancel(baseCtx)

	srv, err := listener.New(tctx, h.listener, h.reg, h.matcher, h.logger)
	if err != nil {
		tcancel()
		return errors.Wrapf(err, "tunnel %q", name)
	}

	c.mu.Lock()
	h.srv = srv
	h.ctx = tctx
	h.cancel = tcancel
	h.done = make(chan struct{})
	c.mu.Unlock()

	c.serveTunnel(h, tctx)
	c.notify()
	c.connectTunnel(h, tctx)

	return nil
}

// Stop cancels the proxy and every still-running tunnel. Returns nil if
// already stopped.
func (c *ProxyController) Stop() error {
	c.mu.Lock()
	if c.state != StateRunning && c.state != StateStarting {
		c.mu.Unlock()
		return nil
	}

	cancel := c.cancel
	done := c.done
	tunnelDones := make([]chan struct{}, 0, len(c.tunnels))

	for _, h := range c.tunnels {
		if h.done != nil {
			tunnelDones = append(tunnelDones, h.done)
		}
	}
	c.mu.Unlock()

	c.logger.Info("stop")

	if cancel != nil {
		cancel()
	}

	// One deadline covers the proxy and every tunnel, so a wedged listener
	// cannot stack ShutdownTimeout per goroutine. State is cleared either
	// way — the contexts are cancelled and the listeners closed, so a
	// straggler goroutine cannot accept new connections.
	timedOut := !waitDone(append([]chan struct{}{done}, tunnelDones...), ShutdownTimeout)

	c.mu.Lock()
	c.tunnels = nil
	c.reg = nil
	c.cfg = nil
	c.baseCtx = nil
	c.mu.Unlock()

	if timedOut {
		c.logger.Warn("stop", "err", errShutdownTimeout)

		return errShutdownTimeout
	}

	return nil
}

// Restart stops then starts. A Stop that times out waiting for a wedged
// listener does not abort the restart: the listener is already closed, so
// Start can rebind, and giving up here would leave the proxy down.
func (c *ProxyController) Restart() error {
	if err := c.Stop(); err != nil && !errors.Is(err, errShutdownTimeout) {
		return err
	}

	return c.Start()
}

// Autoconnect reports whether connections open on demand.
func (c *ProxyController) Autoconnect() bool { return !c.manualOnly.Load() }

// SetAutoconnect sets whether ssh and openconnect upstreams connect on
// demand when a request needs them (the default). With it off, a request
// only uses a connection that is already up; opening one takes a click on
// the upstream's row. Applies immediately when running, and to every Start.
func (c *ProxyController) SetAutoconnect(on bool) {
	c.manualOnly.Store(!on)

	c.mu.Lock()
	reg, cfg := c.reg, c.cfg
	c.mu.Unlock()

	if cfg != nil {
		c.applyAutoconnect(reg, cfg)
	}

	c.notify()
}

// ConnectUpstream opens the named ssh or openconnect upstream's connection
// in the background, undoing a DisconnectUpstream. It returns once the
// attempt has started: connecting can wait on an MFA push or an agent
// approval, which must not hold the menu busy. A failure is logged and
// shows in the upstream's state.
func (c *ProxyController) ConnectUpstream(name string) error {
	d, ctx, err := c.conn(name)
	if err != nil {
		return err
	}

	go func() {
		// Up publishes its states through OnStateChange; the notify after
		// it returns covers the outcome.
		if err := d.Up(ctx); err != nil && ctx.Err() == nil {
			c.logger.Warn("connect upstream", "name", name, "err", err)
		}

		c.notify()
	}()

	return nil
}

// DisconnectUpstream closes the named upstream's connection, aborting a
// connect in flight, and keeps it closed until ConnectUpstream: requests
// routed through it fail rather than silently reconnecting and prompting
// for another approval. Its dependents follow it down: the ssh upstreams
// that reach their bastion through it, and the tunnels pinned to it or to
// one of those, so none of them is left looking up while it cannot work.
func (c *ProxyController) DisconnectUpstream(name string) error {
	d, _, err := c.conn(name)
	if err != nil {
		return err
	}

	c.logger.Info("disconnect upstream", "name", name, "state", d.State())

	c.mu.Lock()
	reg := c.reg
	c.mu.Unlock()

	d.Down()
	c.downDependents(reg, name)

	return nil
}

// upstreamDropped returns the hook for a connection of reg's that went down
// on its own: its dependents follow it down as on DisconnectUpstream, but
// the upstream itself is not held, so it still reconnects on demand. A hook
// from a registry that is no longer the running one does nothing.
func (c *ProxyController) upstreamDropped(reg *upstream.Registry, name string) func() {
	return func() {
		c.mu.Lock()
		current := c.state == StateRunning && c.reg == reg
		c.mu.Unlock()

		if !current {
			return
		}

		c.logger.Warn("upstream dropped", "name", name)
		c.downDependents(reg, name)
	}
}

// downDependents holds down the ssh upstreams that use name as via and
// stops the tunnels pinned to name or to one of those, then repaints.
func (c *ProxyController) downDependents(reg *upstream.Registry, name string) {
	c.mu.Lock()
	if c.cfg == nil || c.reg != reg {
		c.mu.Unlock()
		return
	}

	ups, tunnels := c.dependentsLocked(name)
	c.mu.Unlock()

	if len(ups)+len(tunnels) > 0 {
		c.logger.Info("down dependents", "name", name, "upstreams", ups, "tunnels", tunnels)
	}

	for _, n := range ups {
		if d := c.connDialer(reg, n); d != nil {
			d.Down()
		}
	}

	for _, t := range tunnels {
		if err := c.StopTunnel(t); err != nil {
			c.logger.Warn("down dependents", "name", name, "tunnel", t, "err", err)
		}
	}

	c.notify()
}

// dependentsLocked returns what depends on the named upstream: the ssh
// upstreams whose via it is, and the listening tunnels pinned to it or to
// one of those. Via names a non-ssh upstream, so one level is the whole
// chain. Callers must hold c.mu.
func (c *ProxyController) dependentsLocked(name string) (ups, tunnels []string) { //nolint:nonamedreturns // two same-typed results
	pinned := map[string]bool{name: true}

	for _, n := range connNames(c.cfg) {
		if c.cfg.Upstreams[n].Via == name {
			ups = append(ups, n)
			pinned[n] = true
		}
	}

	for _, t := range slices.Sorted(maps.Keys(c.tunnels)) {
		if h := c.tunnels[t]; h.srv != nil && pinned[h.upstream] {
			tunnels = append(tunnels, t)
		}
	}

	return ups, tunnels
}

// serveTunnel runs h's listener in its own goroutine, using tctx (h's own
// derived context) until it is cancelled (via StopTunnel or the
// whole-controller Stop) or it errors unexpectedly. An unexpected error
// demotes only this tunnel to TunnelStopped — it never touches
// controller-level State or LastError.
//
// Stopping a tunnel's listener stops it from accepting new connections; it
// does not forcibly close connections already accepted (identical to the
// whole-proxy Stop's behavior — see pkg/socks5server/handler.go, which
// never selects on ctx.Done() once a connection is established).
func (c *ProxyController) serveTunnel(h *tunnelHandle, tctx context.Context) {
	go func() {
		defer close(h.done)

		serveErr := h.srv.Serve(tctx)

		c.mu.Lock()
		if cur, ok := c.tunnels[h.name]; ok && cur == h {
			if serveErr != nil && tctx.Err() == nil {
				c.logger.Warn("tunnel", "name", h.name, "err", serveErr)
			}

			cur.srv = nil
			cur.ctx = nil
			cur.cancel = nil
			cur.done = nil
		}
		c.mu.Unlock()
		c.notify()
	}()
}

// connectTunnel brings up h's pinned upstream in the background. Starting
// the tunnel is a request for its whole chain, the way an ssh upstream's Up
// brings up its via: Up clears a Down left on the upstream or its via by an
// earlier disconnect, whether or not autoconnect is on, so the listener is
// never left accepting while what it dials through is held down. A tunnel
// routed by rules, or pinned to an upstream without a connection, costs
// nothing.
//
// tctx is h's own context, so a StopTunnel or a whole-proxy Stop cancels an
// in-flight dial rather than leaving it to run against a tunnel that is
// already gone.
func (c *ProxyController) connectTunnel(h *tunnelHandle, tctx context.Context) {
	d, ok := h.dialer.(connDialer)
	if !ok {
		return
	}

	go func() {
		// Up publishes its states through OnStateChange; the notify after
		// it returns covers the outcome.
		if err := d.Up(tctx); err != nil {
			// Not fatal: the listener stays up and each connection
			// retries the dial lazily. A Down racing this Up stops the
			// tunnel through downDependents.
			if tctx.Err() == nil && !errors.Is(err, sshclient.ErrDisconnected) &&
				!errors.Is(err, openconnect.ErrDisconnected) {
				c.logger.Warn("connect tunnel", "name", h.name, "err", err)
			}
		}

		c.notify()
	}()
}

func (c *ProxyController) fail(err error) {
	c.mu.Lock()
	c.state = StateError
	c.lastErr = err
	c.listen = ""
	c.tunnels = nil
	c.reg = nil
	c.cfg = nil
	c.baseCtx = nil
	c.mu.Unlock()
	c.notify()
}

func (c *ProxyController) notify() {
	c.mu.Lock()
	status := c.statusLocked()
	listeners := append([]func(Status){}, c.listeners...)
	c.mu.Unlock()

	for _, fn := range listeners {
		if fn != nil {
			fn(status)
		}
	}
}

// sshStateChanged returns an SSH dialer's state-change hook: it reports
// entering and leaving ConnAwaitingApproval to onApproval, once per edge,
// then repaints.
func (c *ProxyController) sshStateChanged(state func() sshclient.ConnState) func() {
	var pending atomic.Bool

	return func() {
		p := state() == sshclient.ConnAwaitingApproval
		if pending.Swap(p) != p && c.onApproval != nil {
			c.onApproval(p)
		}

		c.notify()
	}
}

// statusLocked builds a Status snapshot. Callers must hold c.mu.
func (c *ProxyController) statusLocked() Status {
	st := Status{
		State:      c.state,
		ListenAddr: c.listen,
		ConfigPath: c.cfgPath,
		LastError:  c.lastErr,
		Tunnels:    c.tunnelStatusLocked(),
	}

	if c.cfg != nil {
		st.Chrome = c.cfg.Chrome
		st.Forwards = forwardStatus(c.cfg)
		st.Upstreams = c.upstreamStatusLocked()
	}

	return st
}

// forwardStatus lists the config's "forward" upstreams, sorted by name so
// the menu order is stable across rebuilds.
func forwardStatus(cfg *config.Config) []ForwardStatus {
	out := make([]ForwardStatus, 0, len(cfg.Upstreams))

	for _, name := range slices.Sorted(maps.Keys(cfg.Upstreams)) {
		if u := cfg.Upstreams[name]; u.Type == config.UpstreamForward {
			out = append(out, ForwardStatus{Name: name, Addr: u.Addr})
		}
	}

	return out
}

// connDialer is what the menu needs from an upstream with a connection.
type connDialer interface {
	SetAutoconnect(on bool)
	Up(ctx context.Context) error
	Down()
	State() sshclient.ConnState
}

// applyAutoconnect pushes the autoconnect setting to every connection
// upstream in reg.
func (c *ProxyController) applyAutoconnect(reg *upstream.Registry, cfg *config.Config) {
	for _, name := range connNames(cfg) {
		if d := c.connDialer(reg, name); d != nil {
			d.SetAutoconnect(c.Autoconnect())
		}
	}
}

// connNames lists the config's ssh and openconnect upstreams, sorted by
// name so the menu order is stable across rebuilds.
func connNames(cfg *config.Config) []string {
	var out []string

	for _, name := range slices.Sorted(maps.Keys(cfg.Upstreams)) {
		if t := cfg.Upstreams[name].Type; t == config.UpstreamSSH || t == config.UpstreamOpenConnect {
			out = append(out, name)
		}
	}

	return out
}

// upstreamStatusLocked builds the []UpstreamStatus snapshot. Callers must
// hold c.mu.
func (c *ProxyController) upstreamStatusLocked() []UpstreamStatus {
	names := connNames(c.cfg)
	out := make([]UpstreamStatus, 0, len(names))

	for _, name := range names {
		st := UpstreamStatus{Name: name, Type: c.cfg.Upstreams[name].Type}

		if d := c.connDialer(c.reg, name); d != nil {
			st.ConnState = d.State()
		}

		out = append(out, st)
	}

	return out
}

// connDialer returns the named upstream's dialer if it holds a connection,
// else nil. reg may be nil.
func (c *ProxyController) connDialer(reg *upstream.Registry, name string) connDialer {
	if reg == nil {
		return nil
	}

	d, _ := reg.Get(name)
	cd, _ := d.(connDialer)

	return cd
}

// conn returns the named connection upstream and the running proxy's base
// context.
func (c *ProxyController) conn(name string) (connDialer, context.Context, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.state != StateRunning {
		return nil, nil, errors.New("controller not running")
	}

	d := c.connDialer(c.reg, name)
	if d == nil {
		return nil, nil, errors.Errorf("upstream %q: unknown or has no connection", name)
	}

	return d, c.baseCtx, nil
}

// tunnelStatusLocked builds the []TunnelStatus snapshot from c.tunnels.
// Callers must hold c.mu.
func (c *ProxyController) tunnelStatusLocked() []TunnelStatus {
	out := make([]TunnelStatus, 0, len(c.tunnels))

	for _, name := range slices.Sorted(maps.Keys(c.tunnels)) {
		h := c.tunnels[name]

		st := TunnelStatus{Name: name, Upstream: h.upstream, Target: h.listener.Target}
		if h.srv != nil {
			st.State = TunnelListening
			st.Addr = h.srv.Addr()
		} else {
			st.State = TunnelStopped
		}

		out = append(out, st)
	}

	return out
}
