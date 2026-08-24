package sshclient

import (
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/foomo/dockprox/pkg/config"
	"github.com/pkg/errors"
	"golang.org/x/crypto/ssh"
)

const defaultPort = 22

// Target is an SSH endpoint built directly from a config.Upstream. Port
// and User carry their defaults; KeyFile and IdentityAgent have had "~"
// expanded.
type Target struct {
	Host              string
	Port              int
	User              string
	KeyFile           string
	KeyFilePassphrase string
	IdentityAgent     string
	HostKey           string
}

// New builds a Target from an ssh upstream, applying defaults. It does no
// filesystem access beyond resolving the home directory; call Validate for
// that.
func New(u config.Upstream) (*Target, error) {
	t := &Target{
		Host:              u.Host,
		Port:              u.Port,
		User:              u.User,
		KeyFilePassphrase: u.KeyFilePassphrase,
		HostKey:           u.HostKey,
	}

	if t.Port == 0 {
		t.Port = defaultPort
	}

	if t.User == "" {
		me, err := user.Current()
		if err != nil {
			return nil, errors.Wrap(err, "user: no user configured and cannot determine current")
		}

		t.User = me.Username
	}

	var err error
	if t.KeyFile, err = expandHome(u.KeyFile); err != nil {
		return nil, errors.Wrap(err, "keyFile")
	}

	if u.IdentityAgent == config.IdentityAgentEnv {
		t.IdentityAgent = u.IdentityAgent
	} else if t.IdentityAgent, err = expandHome(u.IdentityAgent); err != nil {
		return nil, errors.Wrap(err, "identityAgent")
	}

	return t, nil
}

// Addr returns the dial address "host:port".
func (t *Target) Addr() string {
	return net.JoinHostPort(t.Host, strconv.Itoa(t.Port))
}

// Validate performs the startup checks that config shape validation cannot:
// the key file exists and parses (with the passphrase when supplied), the
// agent socket exists, and a trust source for the host key is available.
// Network errors are deliberately deferred to first use.
func (t *Target) Validate() error {
	if t.KeyFile != "" {
		if _, err := t.keySigner(); err != nil {
			return err
		}
	}

	if _, err := t.agentSocket(); err != nil {
		return err
	}

	if t.HostKey == "" {
		path, err := knownHostsPath()
		if err != nil {
			return err
		}

		if _, err := os.Stat(path); err != nil {
			return errors.Wrapf(err, "hostKey unset and %s unreadable; pin hostKey or run `ssh %s` once", path, t.Host)
		}
	}

	return nil
}

// keySigner reads and parses KeyFile. The distinct passphrase error is the
// one users hit most, so it names the file and the remedy.
func (t *Target) keySigner() (ssh.Signer, error) {
	pem, err := os.ReadFile(t.KeyFile)
	if err != nil {
		return nil, errors.Wrap(err, "keyFile")
	}

	if t.KeyFilePassphrase != "" {
		signer, err := ssh.ParsePrivateKeyWithPassphrase(pem, []byte(t.KeyFilePassphrase))
		return signer, errors.Wrapf(err, "keyFile %s", t.KeyFile)
	}

	signer, err := ssh.ParsePrivateKey(pem)

	var missing *ssh.PassphraseMissingError
	if errors.As(err, &missing) {
		if t.IdentityAgent != "" {
			// The agent can still authenticate; the key is simply unusable.
			return nil, nil //nolint:nilnil // "no signer, no error" is the agent-only path
		}

		return nil, errors.Errorf(
			"keyFile %s is encrypted: set keyFilePassphrase or identityAgent (dockprox never prompts)",
			t.KeyFile)
	}

	return signer, errors.Wrapf(err, "keyFile %s", t.KeyFile)
}

// agentSocket resolves IdentityAgent to a socket path and checks it
// exists. It returns "" when no agent is configured.
func (t *Target) agentSocket() (string, error) {
	path := t.IdentityAgent
	if path == "" {
		return "", nil
	}

	if path == config.IdentityAgentEnv {
		path = os.Getenv(config.IdentityAgentEnv)
		if path == "" {
			return "", errors.New("identityAgent: $SSH_AUTH_SOCK is empty")
		}
	}

	if _, err := os.Stat(path); err != nil { //nolint:gosec // Trusted path
		return "", errors.Wrap(err, "identityAgent")
	}

	return path, nil
}

// expandHome resolves a leading "~/" against the current user's home
// directory. Relative paths are left alone; the process working directory
// resolves them.
func expandHome(path string) (string, error) {
	if path == "" || !strings.HasPrefix(path, "~") {
		return path, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.Wrap(err, "home dir")
	}

	if path == "~" {
		return home, nil
	}

	if !strings.HasPrefix(path, "~/") {
		return "", errors.Errorf("cannot expand %q: only ~/ is supported", path)
	}

	return filepath.Join(home, path[2:]), nil
}

func knownHostsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.Wrap(err, "home dir")
	}

	return filepath.Join(home, ".ssh", "known_hosts"), nil
}
