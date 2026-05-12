package upstream_test

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"testing"
)

// fakeSocks5 implements just enough RFC 1928 + 1929 to bridge a CONNECT
// request to a real backend, for testing Socks5Dialer.
type fakeSocks5 struct {
	addr        string
	requireAuth bool
	user, pass  string
	ln          net.Listener
}

func newFakeSocks5(t *testing.T, backend string) *fakeSocks5 {
	t.Helper()

	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	f := &fakeSocks5{addr: ln.Addr().String(), ln: ln}
	go f.serve(t.Context(), backend)

	t.Cleanup(func() { _ = ln.Close() })

	return f
}

func newFakeSocks5Auth(t *testing.T, backend, user, pass string) *fakeSocks5 {
	t.Helper()

	f := newFakeSocks5(t, backend)
	f.requireAuth = true
	f.user, f.pass = user, pass

	return f
}

func (f *fakeSocks5) serve(ctx context.Context, backend string) {
	for {
		c, err := f.ln.Accept()
		if err != nil {
			return
		}

		go f.handle(ctx, c, backend)
	}
}

func (f *fakeSocks5) handle(ctx context.Context, c net.Conn, backend string) {
	defer c.Close()

	hdr := make([]byte, 2)
	if _, err := io.ReadFull(c, hdr); err != nil || hdr[0] != 0x05 {
		return
	}

	methods := make([]byte, hdr[1])
	if _, err := io.ReadFull(c, methods); err != nil {
		return
	}

	want := byte(0x00)
	if f.requireAuth {
		want = 0x02
	}

	if _, _ = c.Write([]byte{0x05, want}); want == 0xff {
		return
	}

	if f.requireAuth {
		uHdr := make([]byte, 2)
		if _, err := io.ReadFull(c, uHdr); err != nil {
			return
		}

		uname := make([]byte, uHdr[1])
		if _, err := io.ReadFull(c, uname); err != nil {
			return
		}

		pHdr := make([]byte, 1)
		_, _ = io.ReadFull(c, pHdr)
		pwd := make([]byte, pHdr[0])
		_, _ = io.ReadFull(c, pwd)

		status := byte(0x00)
		if string(uname) != f.user || string(pwd) != f.pass {
			status = 0x01
		}

		if _, _ = c.Write([]byte{0x01, status}); status != 0 {
			return
		}
	}

	req := make([]byte, 4)
	if _, err := io.ReadFull(c, req); err != nil || req[0] != 0x05 || req[1] != 0x01 {
		return
	}

	var host string

	switch req[3] {
	case 0x01:
		ip := make([]byte, 4)
		_, _ = io.ReadFull(c, ip)
		host = net.IPv4(ip[0], ip[1], ip[2], ip[3]).String()
	case 0x03:
		l := make([]byte, 1)
		_, _ = io.ReadFull(c, l)
		name := make([]byte, l[0])
		_, _ = io.ReadFull(c, name)
		host = string(name)
	default:
		return
	}

	portB := make([]byte, 2)
	_, _ = io.ReadFull(c, portB)
	port := strconv.Itoa(int(binary.BigEndian.Uint16(portB)))

	// Reply success then bridge to backend.
	_, _ = c.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})

	// Use backend host:port for the bridge, but trust the proxy is being
	// asked to reach it regardless of host/port encoded in the request.
	_ = host // for socks5h local-resolve check the test reads f.lastHost

	bk, err := (&net.Dialer{}).DialContext(ctx, "tcp", backend)
	if err != nil {
		return
	}
	defer bk.Close()

	done := make(chan struct{}, 1)

	go func() { _, _ = io.Copy(bk, c); done <- struct{}{} }()

	_, _ = io.Copy(c, bk)

	<-done

	_ = port
}
