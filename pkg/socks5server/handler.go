package socks5server

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"slices"
	"strconv"
	"time"

	"github.com/pkg/errors"
)

// SOCKS5 reply codes (RFC 1928 §6).
const (
	replySucceeded           = 0x00
	replyHostUnreachable     = 0x04
	replyConnRefused         = 0x05
	replyCommandNotSupported = 0x07
)

func (s *Server) handle(ctx context.Context, client net.Conn) {
	start := time.Now()

	defer client.Close()

	if err := s.negotiateMethod(client); err != nil {
		s.opts.Logger.Warn("handshake", "err", err.Error())
		return
	}

	cmd, host, port, err := readRequest(client)
	if err != nil {
		s.opts.Logger.Warn("request", "err", err.Error())
		return
	}

	if cmd != cmdConnect {
		_ = writeReply(client, replyCommandNotSupported)

		s.opts.Logger.Warn("request", "err", "unsupported command", "cmd", cmd)

		return
	}

	target := net.JoinHostPort(host, strconv.Itoa(port))

	dialer := s.pick(host)

	up, err := dialer.Dial(ctx, target)
	if err != nil {
		_ = writeReply(client, replyCodeFor(err))
		s.opts.Logger.Warn("conn", "host", host, "upstream", dialer.Name(),
			"err", err.Error(), "dur", time.Since(start))

		return
	}
	defer up.Close()

	if err := writeReply(client, replySucceeded); err != nil {
		return
	}

	bytes := spliceClientUpstream(client, up)
	s.opts.Logger.Info("conn", "host", host, "upstream", dialer.Name(),
		"bytes", bytes, "dur", time.Since(start))
}

// negotiateMethod reads the RFC1928 greeting and selects the no-auth
// method (0x00) if offered. Any other offer is rejected.
func (s *Server) negotiateMethod(c net.Conn) error {
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(c, hdr); err != nil {
		return errors.Wrap(err, "read greeting")
	}

	if hdr[0] != socks5Version {
		return errors.Errorf("unsupported version 0x%02x", hdr[0])
	}

	methods := make([]byte, hdr[1])
	if _, err := io.ReadFull(c, methods); err != nil {
		return errors.Wrap(err, "read methods")
	}

	if slices.Contains(methods, byte(methodNoAuth)) {
		_, err := c.Write([]byte{socks5Version, methodNoAuth})
		return err
	}

	_, _ = c.Write([]byte{socks5Version, methodNoAcceptable})

	return errors.New("no acceptable auth method")
}

const (
	socks5Version      = 0x05
	methodNoAuth       = 0x00
	methodNoAcceptable = 0xFF

	cmdConnect = 0x01

	atypIPv4   = 0x01
	atypDomain = 0x03
	atypIPv6   = 0x04
)

// readRequest reads the RFC1928 request following the method greeting and
// returns the command, target host, and port.
func readRequest(c net.Conn) (byte, string, int, error) {
	hdr := make([]byte, 4)
	if _, err := io.ReadFull(c, hdr); err != nil {
		return 0, "", 0, errors.Wrap(err, "read request header")
	}

	if hdr[0] != socks5Version {
		return 0, "", 0, errors.Errorf("unsupported version 0x%02x", hdr[0])
	}

	cmd := hdr[1]

	var host string

	switch hdr[3] {
	case atypIPv4:
		ip := make([]byte, 4)
		if _, err := io.ReadFull(c, ip); err != nil {
			return 0, "", 0, errors.Wrap(err, "read ipv4 addr")
		}

		host = net.IP(ip).String()
	case atypDomain:
		l := make([]byte, 1)
		if _, err := io.ReadFull(c, l); err != nil {
			return 0, "", 0, errors.Wrap(err, "read domain length")
		}

		name := make([]byte, l[0])
		if _, err := io.ReadFull(c, name); err != nil {
			return 0, "", 0, errors.Wrap(err, "read domain")
		}

		host = string(name)
	case atypIPv6:
		ip := make([]byte, 16)
		if _, err := io.ReadFull(c, ip); err != nil {
			return 0, "", 0, errors.Wrap(err, "read ipv6 addr")
		}

		host = net.IP(ip).String()
	default:
		return 0, "", 0, errors.Errorf("unknown atyp 0x%02x", hdr[3])
	}

	pb := make([]byte, 2)
	if _, err := io.ReadFull(c, pb); err != nil {
		return 0, "", 0, errors.Wrap(err, "read port")
	}

	port := int(binary.BigEndian.Uint16(pb))

	return cmd, host, port, nil
}

// writeReply writes an RFC1928 reply with a zero bound address.
func writeReply(c net.Conn, code byte) error {
	_, err := c.Write([]byte{socks5Version, code, 0x00, atypIPv4, 0, 0, 0, 0, 0, 0})
	return err
}

// replyCodeFor maps a dial error to a best-effort SOCKS5 reply code.
func replyCodeFor(err error) byte {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return replyHostUnreachable
	}

	return replyConnRefused
}

func spliceClientUpstream(client net.Conn, up net.Conn) int64 {
	done := make(chan int64, 2)

	go func() {
		n, _ := io.Copy(up, client)
		done <- n
	}()
	go func() {
		n, _ := io.Copy(client, up)
		done <- n
	}()

	a := <-done
	b := <-done

	return a + b
}
