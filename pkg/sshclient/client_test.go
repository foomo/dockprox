package sshclient

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"

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
