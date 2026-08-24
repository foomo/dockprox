package upstream_test

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/log"
	"github.com/foomo/dockprox/pkg/config"
	"github.com/foomo/dockprox/pkg/socks5server"
	"github.com/foomo/dockprox/pkg/upstream"
)

// echoServer accepts one connection at a time and echoes bytes back.
func echoServer(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}

			go func() {
				defer c.Close()

				_, _ = io.Copy(c, c)
			}()
		}
	}()

	return ln.Addr().String()
}

// socks5Connect performs the RFC1928 no-auth greeting and a CONNECT to
// hostPort, returning the reply code.
func socks5Connect(t *testing.T, c net.Conn, hostPort string) byte {
	t.Helper()

	host, portStr, err := net.SplitHostPort(hostPort)
	if err != nil {
		t.Fatal(err)
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := c.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatal(err)
	}

	resp := make([]byte, 2)
	if _, err := io.ReadFull(c, resp); err != nil {
		t.Fatal(err)
	}

	req := []byte{0x05, 0x01, 0x00, 0x03, byte(len(host))}
	req = append(req, host...)

	pb := make([]byte, 2)
	binary.BigEndian.PutUint16(pb, uint16(port))
	req = append(req, pb...)

	if _, err := c.Write(req); err != nil {
		t.Fatal(err)
	}

	head := make([]byte, 4)
	if _, err := io.ReadFull(c, head); err != nil {
		t.Fatal(err)
	}

	switch head[3] {
	case 0x01:
		_, _ = io.ReadFull(c, make([]byte, 6))
	case 0x03:
		n := make([]byte, 1)
		_, _ = io.ReadFull(c, n)
		_, _ = io.ReadFull(c, make([]byte, int(n[0])+2))
	case 0x04:
		_, _ = io.ReadFull(c, make([]byte, 18))
	}

	return head[1]
}

// tunnelListener builds the full chain: an ssh upstream pointed at sshAddr
// with hostFP pinned, exposed through a SOCKS5 listener. Returns the
// listener address.
func tunnelListener(t *testing.T, ctx context.Context, sshAddr, hostFP, keyFile string) string {
	t.Helper()

	host, portStr, err := net.SplitHostPort(sshAddr)
	if err != nil {
		t.Fatal(err)
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}

	d, err := upstream.NewSSH("bastion", config.Upstream{
		Type: config.UpstreamSSH, Host: host, Port: port,
		User: "tester", KeyFile: keyFile, HostKey: hostFP,
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = d.Close() })

	srv, err := socks5server.NewServer(ctx, socks5server.Options{
		Listen: "127.0.0.1:0",
		Dialer: d,
		Logger: log.New(io.Discard),
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = srv.Close() })

	go func() { _ = srv.Serve(ctx) }()

	return srv.Addr()
}

// roundTrip sends a probe through the SOCKS5 listener to echo and asserts
// the bytes come back.
func roundTrip(t *testing.T, ctx context.Context, listenAddr, echoAddr, probe string) {
	t.Helper()

	c, err := (&net.Dialer{}).DialContext(ctx, "tcp", listenAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	if reply := socks5Connect(t, c, echoAddr); reply != 0x00 {
		t.Fatalf("connect reply=%d, want 0x00", reply)
	}

	if _, err := c.Write([]byte(probe)); err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, len(probe))
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatal(err)
	}

	if string(buf) != probe {
		t.Fatalf("echo=%q, want %q", buf, probe)
	}
}

func TestSSHTunnel_RoundTrip(t *testing.T) {
	sshAddr, hostFP, _ := startSSHServer(t)
	echoAddr := echoServer(t)
	key := writeTestKey(t)

	addr := tunnelListener(t, t.Context(), sshAddr, hostFP, key)

	roundTrip(t, t.Context(), addr, echoAddr, "hello through the bastion")
}

func TestSSHTunnel_WrongHostKeyFails(t *testing.T) {
	sshAddr, _, _ := startSSHServer(t)
	echoAddr := echoServer(t)
	key := writeTestKey(t)

	// A syntactically valid fingerprint that is not the server's.
	wrong := "SHA256:" + strings.Repeat("A", 43)

	addr := tunnelListener(t, t.Context(), sshAddr, wrong, key)

	c, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	if reply := socks5Connect(t, c, echoAddr); reply == 0x00 {
		t.Fatal("connect succeeded with a mismatched host key")
	}
}

func TestSSHTunnel_ReconnectsAfterDrop(t *testing.T) {
	sshAddr, hostFP, kill := startSSHServer(t)
	echoAddr := echoServer(t)
	key := writeTestKey(t)

	addr := tunnelListener(t, t.Context(), sshAddr, hostFP, key)

	roundTrip(t, t.Context(), addr, echoAddr, "first")

	kill()

	roundTrip(t, t.Context(), addr, echoAddr, "second")
}
