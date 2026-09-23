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
//
// ctx should carry the whole attempt's deadline: it bounds the dial, and
// its deadline is applied to the socket so it also bounds the signer
// requests the handshake makes later.
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

	signers, cleanup, err := agentSigners(ctx, sock)
	if err != nil {
		return nil, noop, err
	}

	methods = append(methods, ssh.PublicKeysCallback(signers))

	return methods, cleanup, nil
}

// agentSigners dials the agent socket and returns its signer lookup.
//
// The lookup is invoked lazily — during the handshake, long after this
// returns — and talks to the agent over this socket. ctx's cancellation does
// not reach that I/O, so ctx's deadline is applied to the socket itself;
// otherwise an agent that accepts the connection and then never answers (a
// locked vault, a wedged helper) hangs the handshake indefinitely, holding
// Client.mu and queueing every other connection through the upstream.
func agentSigners(ctx context.Context, sock string) (func() ([]ssh.Signer, error), func(), error) {
	var dialer net.Dialer

	conn, err := dialer.DialContext(ctx, "unix", sock)
	if err != nil {
		return nil, func() {}, errors.Wrapf(err, "dial agent %s", sock)
	}

	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}

	return agent.NewClient(conn).Signers, func() { _ = conn.Close() }, nil
}
