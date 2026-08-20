# SSH-backed SOCKS5 tunnels

Date: 2026-08-20
Status: approved, pending implementation plan

## Problem

`dockprox` routes rule-matched hosts through named upstreams. To reach hosts
behind a bastion today, the user runs `ssh -D 1080 -N jumphost` by hand and
points a `type: socks5` upstream at that port (see `docs/guide/usage.md`). The
tunnel's lifecycle is the user's problem: it must be started before dockprox is
useful, it dies on sleep/wake, and nothing in the config records which jump host
a given port belongs to.

This design moves the SSH tunnel into dockprox. The user declares SSH targets in
config; dockprox opens the SSH connections itself and exposes each as a local
SOCKS5 listener, a rule-referenceable upstream, or both.

## Decisions

Settled during brainstorming; recorded because each rules out a plausible
alternative.

| # | Decision | Rejected alternative |
|---|----------|---------------------|
| 1 | Tunnels are both local SOCKS5 listeners **and** rule-referenceable upstreams | listener-only; upstream-only |
| 2 | SSH targets resolve through `~/.ssh/config` + `SSH_AUTH_SOCK` agent | explicit host/user/key fields in dockprox config |
| 3 | Strict `~/.ssh/known_hosts` verification, **no opt-out** | an `insecureSkipHostKeyVerification` escape hatch |
| 4 | Lazy connect: resolve config at startup, defer the SSH handshake to first use | eager connect with a background reconnect supervisor |
| 5 | Drop the global `socks5Listen` added in the previous iteration | keep it alongside tunnel listeners |
| 6 | Tunnels live under `upstreams` as `type: ssh` | a separate top-level `tunnels:` map |

Decision 3 is the security-relevant one: an unverified host key lets anyone who
can MITM the bastion read all tunneled traffic, including credentials for the
internal hosts being proxied to. Registering the key via a normal `ssh <host>`
login is a one-time cost, so no opt-out is offered.

Decision 4's failure mode is deferred *network* errors only. Target resolution
and key-file readability are checked at startup, so a typo in a host alias still
fails fast; only the handshake waits for first use.

## Architecture

Two existing seams carry the whole feature.

### Seam 1: `upstream.Dialer`

`upstream.Dialer` is a two-method interface (`Dial(ctx, hostPort) (net.Conn,
error)` and `Name() string`). `ssh.Client.DialContext` returns a `net.Conn`, so
an SSH tunnel is just another implementation.

New `pkg/upstream/ssh.go` defines `SSHDialer`, holding a lazy
`*sshclient.Client`. `Dial` obtains the SSH client and delegates. `build()` in
`registry.go` gains one `case config.UpstreamSSH`.

Consequence: SSH tunnels work as rule targets with **no changes** to
`pkg/proxy`, `pkg/match`, or `pkg/upstream/registry.go`'s consumers.

### Seam 2: `socks5server.Options.Dialer`

`socks5server` already speaks SOCKS5 over an injected dialer; its `pick(host)`
consults Matcher/Registry per connection. For a tunnel listener, every host uses
one fixed dialer.

Add an optional `Dialer upstream.Dialer` field to `Options`. When set, `pick`
returns it unconditionally, and `Matcher`/`Registry` become optional in
`NewServer`'s validation. `handler.go` — the actual protocol code — is untouched.

### Resulting topology

N tunnels produce N `SSHDialer`s registered under their upstream names, each
optionally paired with a `socks5server.Server` bound to its `socks5Listen`. A
tunnel no rule references and with no `socks5Listen` is dead config and is
rejected by validation.

## New package: `pkg/sshclient`

Isolates SSH concerns so `pkg/upstream` stays transport plumbing.

### `ResolveTarget(alias string) (Target, error)`

Parses `~/.ssh/config` via `github.com/kevinburke/ssh_config`, resolving
`HostName`, `User`, `Port`, `IdentityFile`, and `ProxyJump`. Falls back to
defaults when no entry matches — `User` from the current user, `Port` 22, the
alias itself as hostname — so a bare `host: 10.0.0.5` works with no ssh config
present.

Called for every tunnel at startup. Config errors and unreadable identity files
surface before the process claims to be serving.

### `Client` — lazy connection holder

`Get(ctx) (*ssh.Client, error)`, mutex-guarded:

1. If a client exists, probe it with `SendRequest("keepalive@openssh.com", true,
   nil)`. On success, return it.
2. Otherwise discard any dead client, dial, handshake, store, return.

The mutex collapses concurrent first-use into one handshake: N simultaneous
SOCKS5 accepts during a reconnect wait for a single dial rather than opening N
SSH connections. This is also the reconnect path — there is no separate
supervisor goroutine and no backoff state to go stale across sleep/wake.

### Authentication

Tried in order:

1. Agent via `SSH_AUTH_SOCK` (`golang.org/x/crypto/ssh/agent`).
2. Each resolved `IdentityFile`.

An encrypted key with no agent available produces an error naming the file and
recommending `ssh-add`. No passphrase prompting: a menubar daemon has no
terminal to prompt on.

### Host key verification

`golang.org/x/crypto/ssh/knownhosts` against `~/.ssh/known_hosts`. Unknown or
mismatched keys fail the connection, with the offending fingerprint and the
`ssh <host>` remedy in the error. No opt-out (decision 3).

### ProxyJump

Resolved recursively: dial the jump host's client, `client.DialContext` to the
next hop, then `ssh.NewClientConn` over that conn. The chain is implemented in
full because the recursion is the same code as the single-hop case.

### Dependencies

- `golang.org/x/crypto` — `ssh`, `ssh/agent`, `ssh/knownhosts`
- `github.com/kevinburke/ssh_config` — the maintained fork, `Get`/`GetStrict` API

Both are the standard choices for this work; decision 2 is not implementable
without them. `.golangci.yaml` uses `modules-download-mode: readonly`, so
`make tidy` must run before lint.

## Config

```yaml
upstreams:
  jump:
    type: ssh
    host: jumphost                 # required: ~/.ssh/config alias or hostname
    socks5Listen: 127.0.0.1:1080   # optional: expose a local SOCKS5 port

rules:
  - match: "*.internal.example.com"
    upstream: jump
```

`config.Upstream` gains `Host` and `Socks5Listen`, both `omitempty`. A
`UpstreamSSH = "ssh"` constant joins the existing type constants, and the
`jsonschema:"enum=..."` tag on `Type` gains `enum=ssh`.

### Validation

Added to `Config.Validate` / `Upstream.validate`:

- `type: ssh` requires `host`.
- `socks5Listen`, when set, must parse as `host:port`.
- `socks5Listen` on a non-`ssh` upstream is **rejected** — it would otherwise be
  silently ignored.
- Duplicate `socks5Listen` values across upstreams are **rejected** — otherwise
  the second bind fails at runtime with an opaque error. Requires a
  cross-upstream check in `Config.Validate`, not just per-upstream `validate()`.
- An `ssh` upstream that no rule references and that sets no `socks5Listen` is
  rejected as dead config.

### Removals (decision 5)

- `Config.Socks5Listen` field and its `socks5Listen` schema entry
- its `Validate` branch
- the `--socks5-listen` flag and its `loadConfig` override in `internal/cli/serve.go`
- the Matcher/Registry-routed `socks5server` construction in `runServe`
- the corresponding block in `dockprox.example.yaml`

`pkg/socks5server` itself **stays** — the protocol implementation is what tunnel
listeners are built from. Its existing Matcher/Registry mode also stays, since
that is what makes `Options.Dialer` optional rather than mandatory.

### CLI

`parseUpstreamFlag` gains an `ssh://host` form, for parity with the existing
`socks5://`, `http://`, and `direct` forms. Tunnels declared this way get no
`socks5Listen`; they are rule targets only.

## Lifecycle: `internal/cli/serve.go`

`runServe` currently special-cases one optional SOCKS5 server with a 2-buffered
error channel and hand-rolled teardown. That does not scale to N listeners, so it
becomes an `errgroup.WithContext` over the HTTP proxy plus each tunnel listener.
The first error cancels the group; each server's existing ctx-cancel path closes
its own listener. This shrinks `runServe`.

This is a targeted improvement to code the feature forces a rewrite of, not
unrelated refactoring.

## Lifecycle: `internal/menubar/controller.go`

`ProxyController.Start` duplicates the registry/matcher/server construction and
runs only the HTTP proxy in one goroutine. A menubar user who configures a
tunnel with `socks5Listen` would otherwise get a silently missing port, so the
controller runs tunnel listeners too, under the same errgroup pattern.

`Status.ListenAddr` stays the HTTP proxy address; tunnel listener addresses are
not surfaced in the tray UI in this iteration.

## Testing

| Scope | Coverage |
|-------|----------|
| `pkg/config` | Table-driven validation: ssh without `host`; `socks5Listen` on an `http` upstream; duplicate `socks5Listen`; unreferenced tunnel with no listener; valid tunnel. Plus the existing example-config test. |
| `pkg/sshclient` | `ResolveTarget` against a fixture `ssh_config`: aliases, wildcard `Host` patterns, `ProxyJump`, defaults, missing entry. Pure parsing, no network. |
| `pkg/socks5server` | Existing Matcher-path tests keep passing; one new test for the fixed-`Dialer` path. |
| End-to-end | The test that proves the chain. |

The end-to-end test runs an in-process SSH server (`x/crypto/ssh` supports this)
with a generated host key and a fixture `known_hosts`, points a tunnel at it,
then drives a real SOCKS5 client through the listener to a local echo server and
asserts bytes round-trip. Closing the SSH server's conn mid-test and issuing
another request covers the lazy-reconnect path.

Verification: `make check` (tidy + generate + lint + test.race + audit).
`make generate` regenerates `dockprox.schema.json`; the committed schema must
match.

## Docs

`docs/guide/usage.md` currently instructs the reader to run `ssh -D 1080 -N
jumphost` manually. This feature replaces that workflow, so the page is updated.
`dockprox.example.yaml` gains a documented `type: ssh` upstream and loses the
top-level `socks5Listen` block.

## Out of scope

- Eager connect with keepalives and backoff. Decision 4 chose lazy; the upgrade
  path is a supervisor goroutine calling `Client.Get` on a timer, and it is
  additive.
- Inline `user@host:port` in the `host` field. `~/.ssh/config` is where that
  belongs (decision 2).
- Passphrase prompting for encrypted keys without an agent.
- Surfacing per-tunnel state in the menubar UI.
- Local/remote port forwards (`-L` / `-R`). Only SOCKS5 (`-D`) is in scope.
