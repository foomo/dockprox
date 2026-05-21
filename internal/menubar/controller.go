package menubar

import (
	"context"
	"sync"

	"github.com/charmbracelet/log"
	"github.com/foomo/dockprox/pkg/config"
	"github.com/foomo/dockprox/pkg/match"
	"github.com/foomo/dockprox/pkg/proxy"
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

// Status is a snapshot of controller state.
type Status struct {
	State      State
	ListenAddr string
	ConfigPath string
	LastError  error
}

// ProxyController owns the embedded proxy lifecycle.
type ProxyController struct {
	logger  *log.Logger
	cfgPath string

	mu        sync.Mutex
	state     State
	listen    string
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

	ctx, cancel := context.WithCancel(context.Background())

	srv, err := proxy.NewServer(ctx, proxy.Options{
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

	done := make(chan struct{})

	c.mu.Lock()
	c.cancel = cancel
	c.done = done
	c.listen = srv.Addr()
	c.state = StateRunning
	c.mu.Unlock()
	c.notify()

	go func() {
		defer close(done)
		defer func() {
			if r := recover(); r != nil {
				c.fail(errors.Errorf("proxy panic: %v", r))
			}
		}()

		serveErr := srv.Serve(ctx)

		c.mu.Lock()
		if c.state == StateRunning {
			if serveErr != nil && ctx.Err() == nil {
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

// Stop cancels the proxy. Returns nil if already stopped.
func (c *ProxyController) Stop() error {
	c.mu.Lock()
	if c.state != StateRunning && c.state != StateStarting {
		c.mu.Unlock()
		return nil
	}

	cancel := c.cancel
	done := c.done
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	if done != nil {
		<-done
	}

	return nil
}

// Restart stops then starts.
func (c *ProxyController) Restart() error {
	if err := c.Stop(); err != nil {
		return err
	}

	return c.Start()
}

func (c *ProxyController) fail(err error) {
	c.mu.Lock()
	c.state = StateError
	c.lastErr = err
	c.listen = ""
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
	}
	listeners := append([]func(Status){}, c.listeners...)
	c.mu.Unlock()

	for _, fn := range listeners {
		if fn != nil {
			fn(status)
		}
	}
}
