package cli

import (
	"strings"

	"github.com/charmbracelet/log"
	"github.com/foomo/dockprox/pkg/config"
	"github.com/foomo/dockprox/pkg/match"
	"github.com/foomo/dockprox/pkg/proxy"
	"github.com/foomo/dockprox/pkg/upstream"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
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

	logger := log.NewWithOptions(cmd.ErrOrStderr(), log.Options{ReportTimestamp: true})
	logger.SetLevel(logLevelFromString(cfg.LogLevel))

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

	logger.Info("serve", "listen", srv.Addr(), "upstreams", len(cfg.Upstreams), "rules", len(cfg.Rules))

	return srv.Serve(cmd.Context())
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
	case raw == "direct":
		return name, config.Upstream{Type: config.UpstreamDirect}, nil
	default:
		return "", config.Upstream{}, errors.Errorf("upstream %q: unsupported URL %q", name, raw)
	}
}

func logLevelFromString(s string) log.Level {
	switch s {
	case "debug":
		return log.DebugLevel
	case "warn":
		return log.WarnLevel
	case "error":
		return log.ErrorLevel
	default:
		return log.InfoLevel
	}
}
