package sshclient

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// startHandshakeOnlyServer runs an in-process SSH server that accepts any
// auth and rejects any channel open request. It's enough to exercise
// Client.Get's handshake and keepalive-probe paths without the
// direct-tcpip channel machinery pkg/upstream's end-to-end tests need.
func startHandshakeOnlyServer(t *testing.T) (addr, hostFP string) { //nolint:nonamedreturns
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}

	cfg := &ssh.ServerConfig{
		NoClientAuth: true,
		PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) {
			return &ssh.Permissions{}, nil
		},
	}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			raw, err := ln.Accept()
			if err != nil {
				return
			}

			go serveHandshakeOnlyConn(raw, cfg)
		}
	}()

	return ln.Addr().String(), ssh.FingerprintSHA256(signer.PublicKey())
}

func serveHandshakeOnlyConn(raw net.Conn, cfg *ssh.ServerConfig) {
	sc, chans, reqs, err := ssh.NewServerConn(raw, cfg)
	if err != nil {
		_ = raw.Close()
		return
	}
	defer sc.Close()

	go func() {
		for nc := range chans {
			_ = nc.Reject(ssh.Prohibited, "no channels")
		}
	}()

	for r := range reqs {
		if r.WantReply {
			_ = r.Reply(true, nil)
		}
	}
}

// writeTestKey generates an unencrypted ed25519 private key in a temp dir
// and returns its path. The in-process server accepts any key.
func writeTestKey(t *testing.T) string {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

// testTarget builds a Target dialing addr, with hostFP pinned as the
// trusted host key.
func testTarget(t *testing.T, addr, hostFP string) *Target {
	t.Helper()

	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}

	return &Target{Host: host, Port: port, User: "tester", KeyFile: writeTestKey(t), HostKey: hostFP}
}

func TestClient_State_UnknownBeforeFirstGet(t *testing.T) {
	tgt := &Target{Host: "127.0.0.1", Port: 1}

	c := NewClient(tgt)

	if got := c.State(); got != ConnUnknown {
		t.Fatalf("State()=%v, want ConnUnknown", got)
	}
}

func TestClient_State_DisconnectedAfterFailedDial(t *testing.T) {
	// Port 1 is not listening; the dial should fail fast.
	tgt := &Target{Host: "127.0.0.1", Port: 1, KeyFile: writeTestKey(t)}

	c := NewClient(tgt)

	if _, err := c.Get(t.Context()); err == nil {
		t.Fatal("expected dial to fail")
	}

	if got := c.State(); got != ConnDisconnected {
		t.Fatalf("State()=%v, want ConnDisconnected", got)
	}
}

func TestClient_State_ConnectedAfterSuccessfulGet(t *testing.T) {
	addr, hostFP := startHandshakeOnlyServer(t)
	tgt := testTarget(t, addr, hostFP)

	c := NewClient(tgt)

	if _, err := c.Get(t.Context()); err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got := c.State(); got != ConnConnected {
		t.Fatalf("State()=%v, want ConnConnected", got)
	}

	// A second Get reuses the connection via the keepalive probe and
	// should still report connected.
	if _, err := c.Get(t.Context()); err != nil {
		t.Fatalf("second Get: %v", err)
	}

	if got := c.State(); got != ConnConnected {
		t.Fatalf("State()=%v after second Get, want ConnConnected", got)
	}
}

// TestClient_State_DoesNotBlockOnInFlightGet is the regression test for the
// menu bar freezing while an ssh tunnel's SOCKS5 listener had a connection
// waiting on the handshake. State() is called from the UI thread on every
// menu rebuild; if it took the same mutex Get holds for the length of a
// dial, the whole menu bar stalled until the handshake timed out.
func TestClient_State_DoesNotBlockOnInFlightGet(t *testing.T) {
	// A listener that accepts the TCP connection but never speaks SSH, so
	// Get blocks in the handshake while holding c.mu.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = ln.Close() })

	accepted := make(chan struct{})

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}

		defer conn.Close()

		close(accepted)

		<-t.Context().Done()
	}()

	// Any well-formed fingerprint: the server never gets far enough to
	// present a host key, so the callback is never invoked.
	c := NewClient(testTarget(t, ln.Addr().String(), "SHA256:"+strings.Repeat("A", 43)))

	go func() { _, _ = c.Get(context.Background()) }()

	select {
	case <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("server never accepted the connection")
	}

	done := make(chan ConnState, 1)
	go func() { done <- c.State() }()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("State() blocked behind an in-flight Get")
	}
}

func TestClient_State_ConnectingWhileDialInFlight(t *testing.T) {
	// Accepts the TCP connection but never speaks SSH, so the handshake
	// stays in flight for the duration of the test.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = ln.Close() })

	accepted := make(chan struct{})

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}

		defer conn.Close()

		close(accepted)

		<-t.Context().Done()
	}()

	c := NewClient(testTarget(t, ln.Addr().String(), "SHA256:"+strings.Repeat("A", 43)))

	go func() { _, _ = c.Get(context.Background()) }()

	select {
	case <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("server never accepted the connection")
	}

	// The dial is now parked in the SSH handshake.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if c.State() == ConnConnecting {
			return
		}

		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("State()=%v during an in-flight dial, want ConnConnecting", c.State())
}

func TestClient_MarkConnecting(t *testing.T) {
	c := NewClient(&Target{Host: "127.0.0.1", Port: 1})

	if got := c.State(); got != ConnUnknown {
		t.Fatalf("State()=%v before MarkConnecting, want ConnUnknown", got)
	}

	c.MarkConnecting()

	if got := c.State(); got != ConnConnecting {
		t.Fatalf("State()=%v after MarkConnecting, want ConnConnecting", got)
	}
}

func TestConnState_String(t *testing.T) {
	for _, tc := range []struct {
		state ConnState
		want  string
	}{
		{ConnUnknown, "unknown"},
		{ConnConnected, "connected"},
		{ConnDisconnected, "disconnected"},
		{ConnConnecting, "connecting"},
		{ConnAwaitingApproval, "awaiting approval"},
	} {
		if got := tc.state.String(); got != tc.want {
			t.Errorf("ConnState(%d).String()=%q, want %q", tc.state, got, tc.want)
		}
	}
}

func TestClient_OnStateChange_OnlyOnChange(t *testing.T) {
	c := NewClient(&Target{Host: "127.0.0.1", Port: 1})

	var calls atomic.Int32

	c.OnStateChange(func() { calls.Add(1) })

	c.MarkConnecting()
	c.MarkConnecting()

	if got := calls.Load(); got != 1 {
		t.Fatalf("OnStateChange fired %d times for one transition, want 1", got)
	}
}

// startSilentServer accepts TCP connections and never speaks SSH, so a dial
// parks in the handshake. Each accepted conn is closed once release is
// closed. accepted counts connections.
func startSilentServer(t *testing.T, release <-chan struct{}) (addr string, accepted *atomic.Int32) { //nolint:nonamedreturns
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = ln.Close() })

	accepted = &atomic.Int32{}

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}

			accepted.Add(1)

			go func() {
				defer conn.Close()

				select {
				case <-release:
				case <-t.Context().Done():
				}
			}()
		}
	}()

	return ln.Addr().String(), accepted
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 5s")
		}

		time.Sleep(10 * time.Millisecond)
	}
}

// TestClient_CancelAbortsDial: cancelling ctx (StopTunnel, Stop) must end a
// dial parked in the handshake at once, not after handshakeTimeout.
func TestClient_CancelAbortsDial(t *testing.T) {
	addr, accepted := startSilentServer(t, make(chan struct{}))
	c := NewClient(testTarget(t, addr, "SHA256:"+strings.Repeat("A", 43)))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() { _, err := c.Get(ctx); done <- err }()

	waitFor(t, func() bool { return accepted.Load() == 1 })
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Get succeeded after cancel")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Get still blocked after its ctx was cancelled")
	}
}

// TestClient_QueuedCallsShareFailedDial is the regression test for requests
// queued behind a failing dial each redialing in turn, stacking one full
// timeout per request.
func TestClient_QueuedCallsShareFailedDial(t *testing.T) {
	release := make(chan struct{})
	addr, accepted := startSilentServer(t, release)
	c := NewClient(testTarget(t, addr, "SHA256:"+strings.Repeat("A", 43)))

	first := make(chan error, 1)
	second := make(chan error, 1)

	go func() { _, err := c.Get(context.Background()); first <- err }()

	waitFor(t, func() bool { return accepted.Load() == 1 })

	go func() { _, err := c.Get(context.Background()); second <- err }()

	// Let the second call queue on the mutex before the first dial fails.
	time.Sleep(100 * time.Millisecond)
	close(release)

	for _, ch := range []chan error{first, second} {
		select {
		case err := <-ch:
			if err == nil {
				t.Fatal("Get succeeded against a server that closes the handshake")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Get did not return")
		}
	}

	if got := accepted.Load(); got != 1 {
		t.Fatalf("server accepted %d connections, want 1: the queued call redialed", got)
	}
}

func TestClient_State_DisconnectedAfterClose(t *testing.T) {
	addr, hostFP := startHandshakeOnlyServer(t)
	tgt := testTarget(t, addr, hostFP)

	c := NewClient(tgt)

	if _, err := c.Get(t.Context()); err != nil {
		t.Fatalf("Get: %v", err)
	}

	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if got := c.State(); got != ConnDisconnected {
		t.Fatalf("State()=%v, want ConnDisconnected", got)
	}
}
