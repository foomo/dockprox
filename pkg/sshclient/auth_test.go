package sshclient

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"
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

// TestAgentSigners_DeadlineBoundsStalledAgent is the regression test for a
// stalled agent hanging a dial indefinitely. The signer lookup runs during
// the handshake, long after the auth methods were assembled, so the only
// thing that can bound it is the deadline put on the agent socket.
func TestAgentSigners_DeadlineBoundsStalledAgent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	signers, cleanup, err := agentSigners(ctx, startStalledAgent(t))
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

// TestAgentSigners_NoDeadlineWhenContextHasNone documents that a ctx without
// a deadline leaves the socket unbounded. Client.dial always passes a
// deadline-carrying ctx; this pins the conditional so the fix is not
// silently dependent on that.
func TestAgentSigners_NoDeadlineWhenContextHasNone(t *testing.T) {
	signers, cleanup, err := agentSigners(context.Background(), startStalledAgent(t))
	if err != nil {
		t.Fatalf("agentSigners: %v", err)
	}

	defer cleanup()

	done := make(chan struct{})

	go func() { defer close(done); _, _ = signers() }()

	select {
	case <-done:
		t.Fatal("signers() returned without a deadline; the stalled agent should have blocked it")
	case <-time.After(300 * time.Millisecond):
		// Still blocked, as expected.
	}
}

func TestAuthMethods_AgentOnly(t *testing.T) {
	tgt := &Target{
		Host:          "127.0.0.1",
		Port:          22,
		User:          "tester",
		IdentityAgent: startStalledAgent(t),
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	methods, cleanup, err := tgt.authMethods(ctx)
	if err != nil {
		t.Fatalf("authMethods: %v", err)
	}

	defer cleanup()

	if len(methods) != 1 {
		t.Fatalf("got %d auth methods, want 1 (agent only)", len(methods))
	}
}
