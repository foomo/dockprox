package secret

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
)

const (
	prefixOp       = "op://"
	prefixKeychain = "keychain://"
	prefixCmd      = "cmd:"
	// stderrMax bounds the tool output quoted in an error.
	stderrMax = 200
)

// installHints tell the user how to get a missing tool.
var installHints = map[string]string{
	"op":       "install the 1Password CLI: brew install --cask 1password-cli",
	"security": "keychain:// needs macOS",
}

var defaultResolver atomic.Pointer[Resolver]

func init() {
	defaultResolver.Store(New())
}

// Resolver resolves secret references. Successful lookups are cached for the
// life of the Resolver; failures are not, so a cancelled unlock prompt is
// retried on next use. A Resolver is safe for concurrent use.
type Resolver struct {
	allowCmd   bool
	searchDirs []string

	mu    sync.Mutex
	cache map[string]string
}

// Option configures a Resolver.
type Option func(*Resolver)

// WithCmd enables cmd:<shell> references. They run arbitrary shell, so only
// enable this when references come from trusted config.
func WithCmd() Option {
	return func(r *Resolver) { r.allowCmd = true }
}

// WithSearchDirs adds dirs to look for tools in after $PATH, and appends them
// to the PATH of the child process. Use it when the process may start with a
// minimal PATH, e.g. a macOS app launched from Finder lacks /opt/homebrew/bin.
func WithSearchDirs(dirs ...string) Option {
	return func(r *Resolver) { r.searchDirs = append(r.searchDirs, dirs...) }
}

// New returns a Resolver. Without options it resolves op:// and keychain://
// references and rejects cmd:.
func New(opts ...Option) *Resolver {
	r := &Resolver{cache: map[string]string{}}
	for _, opt := range opts {
		opt(r)
	}

	return r
}

// Default returns the Resolver used by the package-level Check and Resolve.
func Default() *Resolver { return defaultResolver.Load() }

// SetDefault makes r the Resolver used by the package-level Check and Resolve.
func SetDefault(r *Resolver) { defaultResolver.Store(r) }

// Check calls Default().Check.
func Check(s string) error { return Default().Check(s) }

// Resolve calls Default().Resolve.
func Resolve(ctx context.Context, s string) (string, error) { return Default().Resolve(ctx, s) }

// Check reports whether s can be resolved on this machine: it parses the
// reference and finds the tool it needs, without running it, so it does not
// trigger an unlock prompt. Literals and enabled cmd: always pass.
func (r *Resolver) Check(s string) error {
	args, err := r.command(s)
	if err != nil || args == nil {
		return err
	}

	if _, err := r.lookPath(args[0]); err != nil {
		return fmt.Errorf("secret %s: %w", s, err)
	}

	return nil
}

// Resolve returns the secret s refers to, or s itself when it is a literal.
func (r *Resolver) Resolve(ctx context.Context, s string) (string, error) {
	args, err := r.command(s)
	if err != nil {
		return "", err
	}

	if args == nil {
		return s, nil
	}

	r.mu.Lock()
	v, ok := r.cache[s]
	r.mu.Unlock()

	if ok {
		return v, nil
	}

	v, err = r.run(ctx, args)
	if err != nil {
		// The reference names where the secret lives, not the secret.
		return "", fmt.Errorf("secret %s: %w", s, err)
	}

	r.mu.Lock()
	r.cache[s] = v
	r.mu.Unlock()

	return v, nil
}

// command returns the argv that resolves s, or nil when s is a literal.
func (r *Resolver) command(s string) ([]string, error) {
	switch {
	case strings.HasPrefix(s, prefixOp):
		return []string{"op", "read", "--no-newline", s}, nil
	case strings.HasPrefix(s, prefixKeychain):
		service, account, _ := strings.Cut(strings.TrimPrefix(s, prefixKeychain), "/")
		if service == "" || account == "" {
			return nil, fmt.Errorf("secret %q: want keychain://service/account", s)
		}

		return []string{"security", "find-generic-password", "-w", "-s", service, "-a", account}, nil
	case strings.HasPrefix(s, prefixCmd):
		// Don't quote s: a cmd: reference may embed the secret.
		if !r.allowCmd {
			return nil, errors.New("secret cmd: not enabled")
		}

		if strings.TrimSpace(strings.TrimPrefix(s, prefixCmd)) == "" {
			return nil, errors.New("secret cmd: empty command")
		}

		return []string{"/bin/sh", "-c", strings.TrimPrefix(s, prefixCmd)}, nil
	default:
		return nil, nil
	}
}

func (r *Resolver) run(ctx context.Context, args []string) (string, error) {
	bin, err := r.lookPath(args[0])
	if err != nil {
		return "", err
	}

	var stderr bytes.Buffer

	cmd := exec.CommandContext(ctx, bin, args[1:]...)
	if len(r.searchDirs) > 0 {
		// For cmd: scripts, which run their own lookups.
		cmd.Env = append(os.Environ(), "PATH="+strings.Join(append([]string{os.Getenv("PATH")}, r.searchDirs...), ":"))
	}

	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > stderrMax {
			msg = "..." + msg[len(msg)-stderrMax:]
		}

		if msg != "" {
			return "", fmt.Errorf("%s: %w", msg, err)
		}

		return "", err
	}

	return strings.TrimRight(string(out), "\r\n"), nil
}

func (r *Resolver) lookPath(name string) (string, error) {
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}

	for _, dir := range r.searchDirs {
		p := dir + "/" + name
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return p, nil
		}
	}

	err := fmt.Errorf("%s not found in $PATH or %v", name, r.searchDirs)
	if hint, ok := installHints[name]; ok {
		err = fmt.Errorf("%s: %w", hint, err)
	}

	return "", err
}
