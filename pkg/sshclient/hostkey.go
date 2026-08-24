package sshclient

import (
	"net"

	"github.com/pkg/errors"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// hostKeyCallback returns a strict verifier. There is no opt-out: an
// unverified host key would let anyone who can MITM the bastion read all
// tunnelled traffic.
func (t *Target) hostKeyCallback() (ssh.HostKeyCallback, error) {
	if t.HostKey != "" {
		want := t.HostKey

		return func(_ string, _ net.Addr, key ssh.PublicKey) error {
			if got := ssh.FingerprintSHA256(key); got != want {
				return errors.Errorf("host key mismatch for %s: presented %s, pinned %s", t.Host, got, want)
			}

			return nil
		}, nil
	}

	path, err := knownHostsPath()
	if err != nil {
		return nil, err
	}

	cb, err := knownhosts.New(path)
	if err != nil {
		return nil, errors.Wrapf(err, "known_hosts %s", path)
	}

	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		if err := cb(hostname, remote, key); err != nil {
			return errors.Wrapf(err,
				"host key %s for %s not trusted; run `ssh %s` once, or pin hostKey",
				ssh.FingerprintSHA256(key), t.Host, t.Host)
		}

		return nil
	}, nil
}
