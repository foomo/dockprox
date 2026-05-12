package upstream

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"strconv"

	"github.com/foomo/dockprox/pkg/config"
	"github.com/pkg/errors"
)

// Socks5Dialer dials targets through a SOCKS5 upstream. When DNS="remote"
// the proxy resolves the hostname (socks5h behaviour); otherwise the
// hostname is resolved locally before sending atyp=IPv4/IPv6.
type Socks5Dialer struct {
	name string
	cfg  config.Upstream
}

// NewSocks5 returns a Socks5Dialer for the given config.
func NewSocks5(name string, u config.Upstream) *Socks5Dialer {
	return &Socks5Dialer{name: name, cfg: u}
}

// Name implements Dialer.
func (d *Socks5Dialer) Name() string { return d.name }

// Dial implements Dialer.
func (d *Socks5Dialer) Dial(ctx context.Context, hostPort string) (net.Conn, error) {
	host, portStr, err := net.SplitHostPort(hostPort)
	if err != nil {
		return nil, err
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, errors.Wrap(err, "port")
	}

	if port < 0 || port > 0xFFFF {
		return nil, errors.New("port out of range")
	}

	c, err := (&net.Dialer{}).DialContext(ctx, "tcp", d.cfg.Addr)
	if err != nil {
		return nil, errors.Wrap(err, "dial socks5")
	}

	ok := false
	defer func() {
		if !ok {
			_ = c.Close()
		}
	}()

	// Greeting
	method := byte(0x00)
	if d.cfg.Auth != nil {
		method = 0x02
	}

	if _, err := c.Write([]byte{0x05, 0x01, method}); err != nil {
		return nil, err
	}

	hdr := make([]byte, 2)
	if _, err := io.ReadFull(c, hdr); err != nil {
		return nil, err
	}

	if hdr[0] != 0x05 || hdr[1] != method {
		return nil, errors.Errorf("socks5: server selected method %d", hdr[1])
	}

	if d.cfg.Auth != nil {
		if len(d.cfg.Auth.Username) > 255 || len(d.cfg.Auth.Password) > 255 {
			return nil, errors.New("socks5 auth credential too long (RFC 1929 max 255 bytes)")
		}

		req := []byte{0x01}
		req = append(req, byte(len(d.cfg.Auth.Username))) //nolint:gosec // bounded by check above
		req = append(req, d.cfg.Auth.Username...)
		req = append(req, byte(len(d.cfg.Auth.Password))) //nolint:gosec // bounded by check above

		req = append(req, d.cfg.Auth.Password...)
		if _, err := c.Write(req); err != nil {
			return nil, err
		}

		resp := make([]byte, 2)
		if _, err := io.ReadFull(c, resp); err != nil {
			return nil, err
		}

		if resp[1] != 0x00 {
			return nil, errors.Errorf("socks5 auth rejected (code %d)", resp[1])
		}
	}

	// CONNECT
	req := []byte{0x05, 0x01, 0x00}

	if d.cfg.DNS == "remote" {
		if len(host) > 255 {
			return nil, errors.New("hostname too long for socks5")
		}

		req = append(req, 0x03, byte(len(host))) //nolint:gosec // bounded by check above
		req = append(req, host...)
	} else {
		ip := net.ParseIP(host)
		if ip == nil {
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, errors.Wrap(err, "resolve")
			}

			if len(ips) == 0 {
				return nil, errors.Errorf("resolve %q: no addresses", host)
			}

			ip = ips[0].IP
		}

		if v4 := ip.To4(); v4 != nil {
			req = append(req, 0x01)
			req = append(req, v4...)
		} else {
			req = append(req, 0x04)
			req = append(req, ip.To16()...)
		}
	}

	pb := make([]byte, 2)
	binary.BigEndian.PutUint16(pb, uint16(port))

	req = append(req, pb...)
	if _, err := c.Write(req); err != nil {
		return nil, err
	}

	head := make([]byte, 4)
	if _, err := io.ReadFull(c, head); err != nil {
		return nil, err
	}

	if head[1] != 0x00 {
		return nil, errors.Errorf("socks5 connect failed: code %d", head[1])
	}

	switch head[3] {
	case 0x01:
		if _, err := io.ReadFull(c, make([]byte, 6)); err != nil {
			return nil, errors.Wrap(err, "drain ipv4 bound-addr")
		}
	case 0x03:
		n := make([]byte, 1)
		if _, err := io.ReadFull(c, n); err != nil {
			return nil, errors.Wrap(err, "drain domain length")
		}

		if _, err := io.ReadFull(c, make([]byte, int(n[0])+2)); err != nil {
			return nil, errors.Wrap(err, "drain domain bound-addr")
		}
	case 0x04:
		if _, err := io.ReadFull(c, make([]byte, 18)); err != nil {
			return nil, errors.Wrap(err, "drain ipv6 bound-addr")
		}
	default:
		return nil, errors.Errorf("socks5: unknown atyp 0x%02x", head[3])
	}

	ok = true

	return c, nil
}
