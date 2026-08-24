// Package sshclient turns a dockprox ssh upstream into a live SSH
// connection. It owns target defaults, authentication, strict host-key
// verification, and a lazily-established client that reconnects on demand.
//
// Nothing here reads ~/.ssh/config: a Target is built verbatim from the
// config.Upstream fields. ~/.ssh/known_hosts is consulted only as the
// host-key fallback when no fingerprint is pinned.
package sshclient
