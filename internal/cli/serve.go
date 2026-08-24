package cli

import (
	"context"
	"io"
	"maps"
	"net"
	"slices"
	"strconv"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/foomo/dockprox/pkg/config"
	"github.com/foomo/dockprox/pkg/match"
	"github.com/foomo/dockprox/pkg/proxy"
	"github.com/foomo/dockprox/pkg/socks5server"
	"github.com/foomo/dockprox/pkg/upstream"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
)

type serveFlags struct {
	configPath string
	listen     string
	logLevel   string
	upstreams  []string // NAME=URL
	rules      []string // PATTERN=UPSTREAM
}

func newServeCmd() *cobra.Command {
	f := &serveFlags{}
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the proxy server",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runServe(cmd, f)
		},
	}
	cmd.Flags().StringVar(&f.configPath, "config", "", "YAML config file or '-' for stdin")
	cmd.Flags().StringVar(&f.listen, "listen", "", "listen address (overrides config)")
	cmd.Flags().StringVar(&f.logLevel, "log-level", "", "log level: debug|info|warn|error")
	cmd.Flags().StringArrayVar(&f.upstreams, "upstream", nil, "NAME=URL (repeatable)")
	cmd.Flags().StringArrayVar(&f.rules, "rule", nil, "PATTERN=UPSTREAM (repeatable)")

	return cmd
}

func runServe(cmd *cobra.Command, f *serveFlags) error {
	cfg, err := loadConfig(f)
	if err != nil {
		return err
	}

	logFile, err := config.OpenLogWriter(cfg)
	if err != nil {
		return err
	}

	logger := log.NewWithOptions(io.MultiWriter(cmd.ErrOrStderr(), logFile), log.Options{ReportTimestamp: true})
	logger.SetLevel(config.LevelFromString(cfg.LogLevel))

	reg, err := upstream.NewRegistry(cfg)
	if err != nil {
		return errors.Wrap(err, "registry")
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
		return errors.Wrap(err, "matcher")
	}

	srv, err := proxy.NewServer(cmd.Context(), proxy.Options{
		Listen:   cfg.Listen,
		Matcher:  m,
		Registry: reg,
		Logger:   logger,
	})
	if err != nil {
		return err
	}

	tunnels, err := tunnelListeners(cmd.Context(), cfg, reg, logger)
	if err != nil {
		return err
	}

	logger.Info("serve", "listen", srv.Addr(), "upstreams", len(cfg.Upstreams),
		"rules", len(cfg.Rules), "tunnels", len(tunnels))

	g, ctx := errgroup.WithContext(cmd.Context())

	g.Go(func() error { return srv.Serve(ctx) })

	for _, t := range tunnels {
		logger.Info("serve", "socks5Listen", t.Addr())

		g.Go(func() error { return t.Serve(ctx) })
	}

	return g.Wait()
}

// tunnelListeners builds one SOCKS5 listener per ssh upstream that
// declares a socks5Listen. Each is bound to that tunnel's dialer, so every
// connection it accepts goes through that one bastion. Names are sorted so
// startup order and log output are deterministic.
func tunnelListeners(
	ctx context.Context,
	cfg *config.Config,
	reg *upstream.Registry,
	logger *log.Logger,
) ([]*socks5server.Server, error) {
	var servers []*socks5server.Server

	for _, name := range slices.Sorted(maps.Keys(cfg.Upstreams)) {
		u := cfg.Upstreams[name]
		if u.Type != config.UpstreamSSH || u.Socks5Listen == "" {
			continue
		}

		d, ok := reg.Get(name)
		if !ok {
			return nil, errors.Errorf("upstream %q: not in registry", name)
		}

		srv, err := socks5server.NewServer(ctx, socks5server.Options{
			Listen: u.Socks5Listen,
			Dialer: d,
			Logger: logger,
		})
		if err != nil {
			for _, s := range servers {
				_ = s.Close()
			}

			return nil, errors.Wrapf(err, "tunnel %q", name)
		}

		servers = append(servers, srv)
	}

	return servers, nil
}

func loadConfig(f *serveFlags) (*config.Config, error) {
	var (
		cfg *config.Config
		err error
	)

	switch {
	case f.configPath == "-":
		cfg, err = config.LoadStdin()
	case f.configPath != "":
		cfg, err = config.LoadFile(f.configPath)
	default:
		cfg = config.Defaults()
	}

	if err != nil {
		return nil, err
	}

	if f.listen != "" {
		cfg.Listen = f.listen
	}

	if f.logLevel != "" {
		cfg.LogLevel = f.logLevel
	}

	if cfg.Upstreams == nil {
		cfg.Upstreams = map[string]config.Upstream{}
	}

	for _, u := range f.upstreams {
		name, parsed, perr := parseUpstreamFlag(u)
		if perr != nil {
			return nil, perr
		}

		cfg.Upstreams[name] = parsed
	}

	for _, r := range f.rules {
		i := strings.IndexByte(r, '=')
		if i <= 0 {
			return nil, errors.Errorf("rule %q: expected PATTERN=UPSTREAM", r)
		}

		cfg.Rules = append(cfg.Rules, config.Rule{Match: r[:i], Upstream: r[i+1:]})
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func parseUpstreamFlag(s string) (string, config.Upstream, error) {
	i := strings.IndexByte(s, '=')
	if i <= 0 {
		return "", config.Upstream{}, errors.Errorf("upstream %q: expected NAME=URL", s)
	}

	name, raw := s[:i], s[i+1:]
	switch {
	case strings.HasPrefix(raw, "socks5://"):
		return name, config.Upstream{Type: config.UpstreamSocks5, Addr: strings.TrimPrefix(raw, "socks5://")}, nil
	case strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://"):
		return name, config.Upstream{Type: config.UpstreamHTTP, URL: raw}, nil
	case strings.HasPrefix(raw, "ssh://"):
		u, perr := parseSSHUpstream(strings.TrimPrefix(raw, "ssh://"))
		if perr != nil {
			return "", config.Upstream{}, errors.Wrapf(perr, "upstream %q", name)
		}

		return name, u, nil
	case raw == "direct":
		return name, config.Upstream{Type: config.UpstreamDirect}, nil
	default:
		return "", config.Upstream{}, errors.Errorf("upstream %q: unsupported URL %q", name, raw)
	}
}

// parseSSHUpstream parses "host" or "host:port" into an ssh upstream.
// Tunnels declared this way are rule targets only; they get no
// socks5Listen.
func parseSSHUpstream(raw string) (config.Upstream, error) {
	if raw == "" {
		return config.Upstream{}, errors.New("host required")
	}

	host, portStr, err := net.SplitHostPort(raw)
	if err != nil {
		// No port present; treat the whole string as the host.
		return config.Upstream{Type: config.UpstreamSSH, Host: raw}, nil //nolint:nilerr // port is optional
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		return config.Upstream{}, errors.Wrapf(err, "port %q", portStr)
	}

	return config.Upstream{Type: config.UpstreamSSH, Host: host, Port: port}, nil
}
