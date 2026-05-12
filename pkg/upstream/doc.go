// Package upstream defines the Dialer interface used by the dockprox
// proxy server and provides concrete implementations for direct, SOCKS5,
// and HTTP-CONNECT upstreams.
//
// Each Dialer is responsible for producing a net.Conn to the requested
// hostPort target. Dockprox never terminates TLS in tunnel mode; the
// returned conn is wired directly through to the client.
package upstream
