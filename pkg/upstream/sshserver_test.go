package upstream_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/pem"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
)

// directTCPIP is the payload of an RFC 4254 §7.2 direct-tcpip channel
// open request, which is what ssh -D / (*ssh.Client).Dial sends.
type directTCPIP struct {
	DestHost string
	DestPort uint32
	OrigHost string
	OrigPort uint32
}

// startSSHServer runs an in-process SSH server that accepts any
// authentication and forwards direct-tcpip channels to their requested
// target. It returns the listen address, the SHA256 fingerprint of its
// host key, and a func that drops all live connections (for exercising
// reconnect) — the listener stays open.
func startSSHServer(t *testing.T) (string, string, func()) {
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

	var (
		mu    sync.Mutex
		conns []net.Conn
	)

	go func() {
		for {
			raw, err := ln.Accept()
			if err != nil {
				return
			}

			mu.Lock()

			conns = append(conns, raw)
			mu.Unlock()

			go serveSSHConn(raw, cfg)
		}
	}()

	t.Cleanup(func() {
		_ = ln.Close()

		mu.Lock()
		defer mu.Unlock()

		for _, c := range conns {
			_ = c.Close()
		}
	})

	kill := func() {
		mu.Lock()
		defer mu.Unlock()

		for _, c := range conns {
			_ = c.Close()
		}

		conns = nil
	}

	return ln.Addr().String(), ssh.FingerprintSHA256(signer.PublicKey()), kill
}

func serveSSHConn(raw net.Conn, cfg *ssh.ServerConfig) {
	sc, chans, reqs, err := ssh.NewServerConn(raw, cfg)
	if err != nil {
		_ = raw.Close()
		return
	}
	defer sc.Close()

	// Answer keepalive@openssh.com so the client's liveness probe succeeds.
	go func() {
		for r := range reqs {
			if r.WantReply {
				_ = r.Reply(false, nil)
			}
		}
	}()

	for nc := range chans {
		if nc.ChannelType() != "direct-tcpip" {
			_ = nc.Reject(ssh.UnknownChannelType, nc.ChannelType())
			continue
		}

		var payload directTCPIP
		if err := ssh.Unmarshal(nc.ExtraData(), &payload); err != nil {
			_ = nc.Reject(ssh.ConnectionFailed, err.Error())
			continue
		}

		target := net.JoinHostPort(payload.DestHost, portString(payload.DestPort))

		up, err := net.Dial("tcp", target)
		if err != nil {
			_ = nc.Reject(ssh.ConnectionFailed, err.Error())
			continue
		}

		ch, chReqs, err := nc.Accept()
		if err != nil {
			_ = up.Close()
			continue
		}

		go ssh.DiscardRequests(chReqs)

		go func() {
			defer ch.Close()
			defer up.Close()

			done := make(chan struct{}, 2)

			go func() { _, _ = io.Copy(up, ch); done <- struct{}{} }()
			go func() { _, _ = io.Copy(ch, up); done <- struct{}{} }()

			<-done
		}()
	}
}

func portString(p uint32) string {
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, uint16(p))

	return strconv.Itoa(int(binary.BigEndian.Uint16(b)))
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
