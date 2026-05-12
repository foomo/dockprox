// Package proxy implements an HTTP(S) proxy server that bypasses
// non-matched destinations directly and forwards matched destinations
// through named upstream Dialers.
//
// The server accepts HTTP CONNECT for HTTPS tunnelling and absolute-URI
// HTTP requests for plain HTTP forwarding. It never terminates client
// TLS in tunnel mode.
package proxy
