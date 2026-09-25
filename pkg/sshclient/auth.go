package sshclient

import (
	"context"
	"io"
	"net"
	"time"

	"github.com/charmbracelet/log"
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
// ctx bounds the agent dial; att bounds every request on the agent socket
// after it, including the signer requests the handshake makes later.
func (t *Target) authMethods(ctx context.Context, att *attempt) ([]ssh.AuthMethod, func(), error) {
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

	signers, cleanup, err := agentSigners(ctx, sock, att)
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
// not reach that I/O, so the socket joins att and carries its deadline;
// otherwise an agent that accepts the connection and then never answers (a
// locked vault, a wedged helper) hangs the handshake indefinitely, holding
// Client.mu and queueing every other connection through the upstream.
//
// Sign requests get att's longer approval window: 1Password suppresses its
// prompt for background apps, and the user has to open it from the
// 1Password menu before it can be answered.
func agentSigners(ctx context.Context, sock string, att *attempt) (func() ([]ssh.Signer, error), func(), error) {
	var dialer net.Dialer

	conn, err := dialer.DialContext(ctx, "unix", sock)
	if err != nil {
		return nil, func() {}, errors.Wrapf(err, "dial agent %s", sock)
	}

	att.add(conn)

	ag := agent.NewClient(conn)

	signers := func() ([]ssh.Signer, error) {
		start := time.Now()

		log.Debug("agent list", "sock", sock)

		ss, err := ag.Signers()
		log.Debug("agent list done", "sock", sock, "keys", len(ss), "dur", time.Since(start), "err", err)

		for i, s := range ss {
			if as, ok := s.(ssh.AlgorithmSigner); ok {
				ss[i] = agentSigner{AlgorithmSigner: as, sock: sock, att: att}
			}
		}

		return ss, err
	}

	return signers, func() { _ = conn.Close() }, nil
}

// agentSigner gives agent sign requests the approval window and logs their
// timing. It embeds ssh.AlgorithmSigner so RSA SHA-2 negotiation keeps
// working.
type agentSigner struct {
	ssh.AlgorithmSigner

	sock string
	att  *attempt
}

func (s agentSigner) Sign(rand io.Reader, data []byte) (*ssh.Signature, error) {
	return s.SignWithAlgorithm(rand, data, "")
}

func (s agentSigner) SignWithAlgorithm(rand io.Reader, data []byte, algorithm string) (*ssh.Signature, error) {
	fp := ssh.FingerprintSHA256(s.PublicKey())
	start := time.Now()

	log.Debug("agent sign", "sock", s.sock, "key", fp, "algo", algorithm)

	s.att.signStart()
	sig, err := s.AlgorithmSigner.SignWithAlgorithm(rand, data, algorithm)
	s.att.signDone()

	log.Debug("agent sign done", "sock", s.sock, "key", fp, "dur", time.Since(start), "err", err)

	return sig, err
}
