package upstream_test

import (
	"encoding/binary"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/foomo/dockprox/pkg/config"
	"github.com/foomo/dockprox/pkg/upstream"
)

// TestOpenConnectDialer_TunResolvesWithVPNDNS runs the tun mode against
// fake sudo, openconnect and vpnc-script scripts on PATH: openconnect runs
// the --script with the connect reason, naming a fake nameserver that
// answers every A query with 127.0.0.1, then blocks; sudo runs whatever it
// is given. Dial of a name only that nameserver knows has to reach a
// loopback listener without a SOCKS hop.
func TestOpenConnectDialer_TunResolvesWithVPNDNS(t *testing.T) {
	dir := t.TempDir()
	dns := fakeDNS(t, net.IPv4(127, 0, 0, 1))

	for name, script := range map[string]string{
		"sudo": "#!/bin/sh\nif [ \"$2\" = \"-l\" ]; then exit 0; fi\nshift\nexec \"$@\"\n",
		"openconnect": "#!/bin/sh\n" +
			"if [ \"$1\" = --version ]; then echo 'Default vpnc-script (override with --script): " + filepath.Join(dir, "vpnc-script") + "'; exit 0; fi\n" +
			"read -r _\n" +
			"while [ $# -gt 0 ]; do if [ \"$1\" = --script ]; then script=$2; fi; shift; done\n" +
			"reason=connect INTERNAL_IP4_DNS='" + dns + "' /bin/sh -c \"$script\" || exit 1\n" +
			"exec sleep 60\n",
		"vpnc-script": "#!/bin/sh\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	d, err := upstream.NewOpenConnect("vpn", config.Upstream{
		Type: config.UpstreamOpenConnect, Server: "https://vpn.example.com", Tun: true,
		Auth: &config.UpstreamAuth{Username: "u", Password: "p"},
	})
	if err != nil {
		t.Fatalf("NewOpenConnect: %v", err)
	}

	t.Cleanup(func() { _ = d.Close() })

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	accepted := make(chan struct{})

	go func() {
		if c, err := ln.Accept(); err == nil {
			_ = c.Close()
		}

		close(accepted)
	}()

	_, port, _ := net.SplitHostPort(ln.Addr().String())

	conn, err := d.Dial(t.Context(), "target.vpn.internal:"+port)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	<-accepted

	if got := conn.RemoteAddr().String(); got != ln.Addr().String() {
		t.Fatalf("dialed %s, want %s direct", got, ln.Addr())
	}
}

// fakeDNS serves UDP DNS on loopback, answering every A query with ip and
// everything else with an empty answer. It returns its "host:port".
func fakeDNS(t *testing.T, ip net.IP) string {
	t.Helper()

	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = pc.Close() })

	go func() {
		buf := make([]byte, 512)

		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}

			// Header is 12 bytes; the question is a length-prefixed name
			// ending in a zero byte, then type and class.
			i := 12
			for i < n && buf[i] != 0 {
				i += int(buf[i]) + 1
			}

			i += 5
			if i > n {
				continue
			}

			resp := append([]byte{}, buf[:i]...)
			resp[2], resp[3] = 0x81, 0x80 // response, recursion available
			clear(resp[6:12])             // no answers, authorities or additionals

			if qtype := binary.BigEndian.Uint16(resp[i-4:]); qtype == 1 {
				resp = append(resp, 0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4) // name pointer, A, IN, TTL, length
				resp = append(resp, ip.To4()...)
				binary.BigEndian.PutUint16(resp[6:], 1)
			}

			_, _ = pc.WriteTo(resp, addr)
		}
	}()

	return pc.LocalAddr().String()
}
