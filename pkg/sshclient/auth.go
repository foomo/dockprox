package sshclient

import (
	"context"
	"net"

	"github.com/pkg/errors"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// authMethods assembles the auth methods for one connection attempt: the
// key file first, then the agent. The returned cleanup closes the agent
// conn and must be called once the handshake completes.
//
// The agent socket is dialled per attempt rather than held: agents come and
// go (a laptop's agent restarts, a socket is remounted) and a cached conn
// would outlive them.
func (t *Target) authMethods(ctx context.Context) ([]ssh.AuthMethod, func(), error) {
	noop := func() {}

	var methods []ssh.AuthMethod

	if t.KeyFile != "" {
		signer, err := t.keySigner()
		if err != nil {
			return nil, noop, err
		}

		if signer != nil {
			methods = append(methods, ssh.PublicKeys(signer))
		}
	}

	sock, err := t.agentSocket()
	if err != nil {
		return nil, noop, err
	}

	if sock == "" {
		if len(methods) == 0 {
			return nil, noop, errors.New("no usable auth method: set keyFile or identityAgent")
		}

		return methods, noop, nil
	}

	var dialer net.Dialer

	conn, err := dialer.DialContext(ctx, "unix", sock)
	if err != nil {
		return nil, noop, errors.Wrapf(err, "dial agent %s", sock)
	}

	methods = append(methods, ssh.PublicKeysCallback(agent.NewClient(conn).Signers))

	return methods, func() { _ = conn.Close() }, nil
}
