package secret

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/pkg/errors"
)

const (
	prefixOp       = "op://"
	prefixKeychain = "keychain://"
	prefixCmd      = "cmd:"
	// stderrMax bounds the tool output quoted in an error.
	stderrMax = 200
)

// searchDirs are appended to the child's PATH: an app launched from Finder
// gets a minimal PATH without the Homebrew prefixes, where op lives.
var searchDirs = []string{"/opt/homebrew/bin", "/usr/local/bin"}

var (
	mu    sync.Mutex
	cache = map[string]string{}
)

// installHints tell the user how to get a missing tool.
var installHints = map[string]string{
	"op":       "install the 1Password CLI: brew install --cask 1password-cli",
	"security": "keychain:// needs macOS",
}

// Check reports whether s can be resolved on this machine: it parses the
// reference and finds the tool it needs, without running it, so it does not
// trigger an unlock prompt. Literals and cmd: always pass.
func Check(s string) error {
	args, err := command(s)
	if err != nil || args == nil {
		return err
	}

	_, err = lookPath(args[0])

	return errors.Wrapf(err, "secret %s", s)
}

// Resolve returns the secret s refers to, or s itself when it is a literal.
// Successful lookups are cached for the life of the process; failures are
// not, so a cancelled unlock prompt is retried on next use.
func Resolve(ctx context.Context, s string) (string, error) {
	args, err := command(s)
	if err != nil {
		return "", err
	}

	if args == nil {
		return s, nil
	}

	mu.Lock()
	v, ok := cache[s]
	mu.Unlock()

	if ok {
		return v, nil
	}

	v, err = run(ctx, args)
	if err != nil {
		// The reference names where the secret lives, not the secret.
		return "", errors.Wrapf(err, "secret %s", s)
	}

	mu.Lock()
	cache[s] = v
	mu.Unlock()

	return v, nil
}

// command returns the argv that resolves s, or nil when s is a literal.
func command(s string) ([]string, error) {
	switch {
	case strings.HasPrefix(s, prefixOp):
		return []string{"op", "read", "--no-newline", s}, nil
	case strings.HasPrefix(s, prefixKeychain):
		service, account, _ := strings.Cut(strings.TrimPrefix(s, prefixKeychain), "/")
		if service == "" || account == "" {
			return nil, errors.Errorf("secret %q: want keychain://service/account", s)
		}

		return []string{"security", "find-generic-password", "-w", "-s", service, "-a", account}, nil
	case strings.HasPrefix(s, prefixCmd):
		if strings.TrimSpace(strings.TrimPrefix(s, prefixCmd)) == "" {
			return nil, errors.New("secret cmd: empty command")
		}

		return []string{"/bin/sh", "-c", strings.TrimPrefix(s, prefixCmd)}, nil
	default:
		return nil, nil
	}
}

func run(ctx context.Context, args []string) (string, error) {
	bin, err := lookPath(args[0])
	if err != nil {
		return "", err
	}

	var stderr bytes.Buffer

	cmd := exec.CommandContext(ctx, bin, args[1:]...)
	// For cmd: scripts, which run their own lookups.
	cmd.Env = append(os.Environ(), "PATH="+strings.Join(append([]string{os.Getenv("PATH")}, searchDirs...), ":"))
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > stderrMax {
			msg = "..." + msg[len(msg)-stderrMax:]
		}

		if msg != "" {
			return "", errors.Wrap(err, msg)
		}

		return "", err
	}

	return strings.TrimRight(string(out), "\r\n"), nil
}

func lookPath(name string) (string, error) {
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}

	for _, dir := range searchDirs {
		p := dir + "/" + name
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return p, nil
		}
	}

	err := errors.Errorf("%s not found in $PATH or %v", name, searchDirs)
	if hint, ok := installHints[name]; ok {
		err = errors.Wrap(err, hint)
	}

	return "", err
}
