// Package socks5server implements a minimal inbound SOCKS5 server (RFC
// 1928). CONNECT requests are routed either through the Matcher/Registry
// used by pkg/proxy, or — when Options.Dialer is set — through that single
// dialer, which is how ssh tunnel listeners are built. Only the no-auth
// method and the CONNECT command are supported.
package socks5server
