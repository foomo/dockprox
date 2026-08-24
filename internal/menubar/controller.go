package menubar

import (
	"context"
	"maps"
	"slices"
	"sync"

	"github.com/charmbracelet/log"
	"github.com/foomo/dockprox/pkg/config"
	"github.com/foomo/dockprox/pkg/match"
	"github.com/foomo/dockprox/pkg/proxy"
	"github.com/foomo/dockprox/pkg/socks5server"
	"github.com/foomo/dockprox/pkg/sshclient"
	"github.com/foomo/dockprox/pkg/upstream"
	"github.com/pkg/errors"
)

// State is the proxy lifecycle state.
type State int

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
}

// TunnelStatus describes one ssh tunnel's SOCKS5 listener and SSH
// connection state.
type TunnelStatus struct {
	Name string
	// Addr is the listener's bound address, or "" when State is
	// TunnelStopped.
	Addr string
	// State reflects whether this tunnel's SOCKS5 listener is currently
	// accepting connections.
	State TunnelState
	// ConnState is the last known state of the underlying SSH connection:
	// ConnUnknown means no dial has been attempted yet (the lazy-connect
	// idle state), distinct from ConnDisconnected (a dial was attempted
	// and failed, or the client was closed). It is a passive signal (see
	// sshclient.Client.State) and is always ConnUnknown when State is
	// TunnelStopped.
	ConnState sshclient.ConnState
}

// tunnelHandle is the controller's live bookkeeping for one tunnel,
// independent of the HTTP proxy and other tunnels. srv/ctx/cancel/done are
// nil when the tunnel is stopped.
type tunnelHandle struct {
	name   string
	dialer upstream.Dialer
	listen string

	srv    *socks5server.Server
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

	return Status{
		State:      c.state,
		ListenAddr: c.listen,
		ConfigPath: c.cfgPath,
		LastError:  c.lastErr,
		Tunnels:    c.tunnelStatusLocked(),
	}
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

	tunnels, err := buildTunnels(baseCtx, cfg, reg, c.logger)
	if err != nil {
		cancel()
		c.fail(errors.Wrap(err, "tunnels"))

		return err
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

	for _, h := range tunnels {
		c.serveTunnel(h, h.ctx)
	}

	go func() {
		defer close(done)
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

// buildTunnels builds one SOCKS5 listener per ssh upstream that declares a
// socks5Listen. Each is bound to that tunnel's dialer, so every connection
// it accepts goes through that one bastion. Names are sorted so startup
// order and log output are deterministic.
func buildTunnels(
	ctx context.Context,
	cfg *config.Config,
	reg *upstream.Registry,
	logger *log.Logger,
) (map[string]*tunnelHandle, error) {
	tunnels := make(map[string]*tunnelHandle)

	for _, name := range slices.Sorted(maps.Keys(cfg.Upstreams)) {
		u := cfg.Upstreams[name]
		if u.Type != config.UpstreamSSH || u.Socks5Listen == "" {
			continue
		}

		d, ok := reg.Get(name)
		if !ok {
			return nil, errors.Errorf("upstream %q: not in registry", name)
		}

		tctx, tcancel := context.WithCancel(ctx)

		srv, err := socks5server.NewServer(tctx, socks5server.Options{
			Listen: u.Socks5Listen,
			Dialer: d,
			Logger: logger,
		})
		if err != nil {
			tcancel()

			for _, h := range tunnels {
				_ = h.srv.Close()
			}

			return nil, errors.Wrapf(err, "tunnel %q", name)
		}

		tunnels[name] = &tunnelHandle{
			name:   name,
			dialer: d,
			listen: u.Socks5Listen,
			srv:    srv,
			ctx:    tctx,
			cancel: tcancel,
			done:   make(chan struct{}),
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

	if done != nil {
		<-done
	}

	return nil
}

// StartTunnel rebinds the named tunnel's SOCKS5 listener, reusing the same
// upstream's dialer (and its underlying SSH connection state) from the
// running registry. Idempotent if already running.
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

	srv, err := socks5server.NewServer(tctx, socks5server.Options{
		Listen: h.listen,
		Dialer: h.dialer,
		Logger: c.logger,
	})
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

	if cancel != nil {
		cancel()
	}

	if done != nil {
		<-done
	}

	for _, d := range tunnelDones {
		<-d
	}

	c.mu.Lock()
	c.tunnels = nil
	c.reg = nil
	c.cfg = nil
	c.baseCtx = nil
	c.mu.Unlock()

	return nil
}

// Restart stops then starts.
func (c *ProxyController) Restart() error {
	if err := c.Stop(); err != nil {
		return err
	}

	return c.Start()
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
	status := Status{
		State:      c.state,
		ListenAddr: c.listen,
		ConfigPath: c.cfgPath,
		LastError:  c.lastErr,
		Tunnels:    c.tunnelStatusLocked(),
	}
	listeners := append([]func(Status){}, c.listeners...)
	c.mu.Unlock()

	for _, fn := range listeners {
		if fn != nil {
			fn(status)
		}
	}
}

// tunnelStatusLocked builds the []TunnelStatus snapshot from c.tunnels.
// Callers must hold c.mu.
func (c *ProxyController) tunnelStatusLocked() []TunnelStatus {
	out := make([]TunnelStatus, 0, len(c.tunnels))

	for _, name := range slices.Sorted(maps.Keys(c.tunnels)) {
		h := c.tunnels[name]

		st := TunnelStatus{Name: name}
		if h.srv != nil {
			st.State = TunnelListening
			st.Addr = h.srv.Addr()
		} else {
			st.State = TunnelStopped
		}

		if sd, ok := h.dialer.(*upstream.SSHDialer); ok {
			st.ConnState = sd.State()
		}

		out = append(out, st)
	}

	return out
}
