package sshclient

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// startStalledAgent listens on a unix socket that accepts connections and
// then never replies, standing in for a locked or wedged 1Password agent.
func startStalledAgent(t *testing.T) string {
	t.Helper()

	// Keep the path short: unix socket paths are capped near 104 bytes, and
	// t.TempDir() under a long test name can approach that.
	sock := filepath.Join(t.TempDir(), "a.sock")

	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}

			go func() {
				defer conn.Close()

				<-t.Context().Done()
			}()
		}
	}()

	return sock
}

// serveAgent serves a on a fresh unix socket and returns its path.
func serveAgent(t *testing.T, a agent.Agent) string {
	t.Helper()

	sock := filepath.Join(t.TempDir(), "a.sock")

	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}

			go func() { _ = agent.ServeAgent(a, conn) }()
		}
	}()

	return sock
}

// signStallingAgent lists its keys but never answers a sign request, like
// 1Password holding a request behind a suppressed approval prompt.
type signStallingAgent struct {
	agent.Agent

	done <-chan struct{}
}

func (a signStallingAgent) Sign(ssh.PublicKey, []byte) (*ssh.Signature, error) {
	<-a.done
	return nil, errors.New("stalled")
}

// TestAgentSigners_DeadlineBoundsStalledAgent is the regression test for a
// stalled agent hanging a dial indefinitely. The signer lookup runs during
// the handshake, long after the auth methods were assembled, so the only
// thing that can bound it is the deadline put on the agent socket.
func TestAgentSigners_DeadlineBoundsStalledAgent(t *testing.T) {
	signers, cleanup, err := agentSigners(context.Background(), startStalledAgent(t),
		newAttempt(250*time.Millisecond, time.Minute))
	if err != nil {
		t.Fatalf("agentSigners: %v", err)
	}

	defer cleanup()

	done := make(chan error, 1)

	go func() { _, err := signers(); done <- err }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("signers() succeeded against an agent that never replies")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("signers() hung: the agent socket carries no deadline")
	}
}

// TestAgentSigners_SignGetsApprovalWindow pins the fix for 1Password's
// suppressed prompt: while a sign request is pending the deadline is the
// approval window, not the handshake timeout, and the pending state is
// reported so the menu bar can tell the user to approve it.
func TestAgentSigners_SignGetsApprovalWindow(t *testing.T) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	keyring := agent.NewKeyring()
	if err := keyring.Add(agent.AddedKey{PrivateKey: key}); err != nil {
		t.Fatal(err)
	}

	sock := serveAgent(t, signStallingAgent{Agent: keyring, done: t.Context().Done()})

	const timeout, approval = 100 * time.Millisecond, 600 * time.Millisecond

	att := newAttempt(timeout, approval)

	var (
		mu     sync.Mutex
		events []bool
	)

	att.onSign = func(pending bool) {
		mu.Lock()
		defer mu.Unlock()

		events = append(events, pending)
	}

	signers, cleanup, err := agentSigners(context.Background(), sock, att)
	if err != nil {
		t.Fatalf("agentSigners: %v", err)
	}

	defer cleanup()

	ss, err := signers()
	if err != nil || len(ss) != 1 {
		t.Fatalf("signers() = %d, %v; want 1 signer", len(ss), err)
	}

	start := time.Now()

	if _, err := ss[0].Sign(rand.Reader, []byte("payload")); err == nil {
		t.Fatal("sign succeeded against an agent that never answers it")
	}

	if elapsed := time.Since(start); elapsed < approval-100*time.Millisecond || elapsed > 5*time.Second {
		t.Fatalf("sign gave up after %v; want about the %v approval window", elapsed, approval)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(events) != 2 || !events[0] || events[1] {
		t.Fatalf("onSign events = %v; want [true false]", events)
	}
}

// TestAttempt_AbortUnblocksAgent covers cancellation: ctx does not reach
// I/O on an already-dialled socket, so abort must expire its deadline.
func TestAttempt_AbortUnblocksAgent(t *testing.T) {
	att := newAttempt(time.Minute, time.Minute)

	signers, cleanup, err := agentSigners(context.Background(), startStalledAgent(t), att)
	if err != nil {
		t.Fatalf("agentSigners: %v", err)
	}

	defer cleanup()

	done := make(chan error, 1)

	go func() { _, err := signers(); done <- err }()

	time.Sleep(50 * time.Millisecond)
	att.abort()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("signers() succeeded after abort")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("signers() still blocked after abort")
	}
}

// TestAgentSigners_KeepsRSASHA2 guards the signer wrapper: it must still be
// an ssh.AlgorithmSigner, or RSA auth falls back to SHA-1 ssh-rsa.
func TestAgentSigners_KeepsRSASHA2(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	keyring := agent.NewKeyring()
	if err := keyring.Add(agent.AddedKey{PrivateKey: key}); err != nil {
		t.Fatal(err)
	}

	signers, cleanup, err := agentSigners(context.Background(), serveAgent(t, keyring),
		newAttempt(5*time.Second, 5*time.Second))
	if err != nil {
		t.Fatalf("agentSigners: %v", err)
	}

	defer cleanup()

	ss, err := signers()
	if err != nil || len(ss) != 1 {
		t.Fatalf("signers() = %d, %v; want 1 signer", len(ss), err)
	}

	as, ok := ss[0].(ssh.AlgorithmSigner)
	if !ok {
		t.Fatal("agent signer lost ssh.AlgorithmSigner")
	}

	data := []byte("payload")

	sig, err := as.SignWithAlgorithm(rand.Reader, data, ssh.KeyAlgoRSASHA256)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	if sig.Format != ssh.KeyAlgoRSASHA256 {
		t.Fatalf("signature format %q, want %q", sig.Format, ssh.KeyAlgoRSASHA256)
	}

	if err := as.PublicKey().Verify(data, sig); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestAuthMethods_AgentOnly(t *testing.T) {
	tgt := &Target{
		Host:          "127.0.0.1",
		Port:          22,
		User:          "tester",
		IdentityAgent: startStalledAgent(t),
	}

	methods, cleanup, err := tgt.authMethods(context.Background(), newAttempt(time.Second, time.Second))
	if err != nil {
		t.Fatalf("authMethods: %v", err)
	}

	defer cleanup()

	if len(methods) != 1 {
		t.Fatalf("got %d auth methods, want 1 (agent only)", len(methods))
	}
}
