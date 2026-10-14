// Package openconnect runs a rootless VPN for a dockprox openconnect
// upstream. It spawns openconnect with --script-tun and ocproxy as the
// script, so the VPN terminates in a userspace TCP/IP stack that exposes a
// local SOCKS5 port: no tun device, no system routes, no root.
//
// The process is started lazily on first use, shared by every caller, and
// restarted on demand after it exits.
package openconnect
