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
| 2 | SSH targets are declared **inline** in dockprox config: `host`, `port`, `user`, `keyFile`, `keyFilePassphrase`, `agent` | resolving them through `~/.ssh/config` |
| 3 | Strict host key verification, **no opt-out** | an `insecureSkipHostKeyVerification` escape hatch |
| 3a | Trusted key from an inline `hostKey` fingerprint, falling back to `~/.ssh/known_hosts` when unset | `known_hosts` only; a configurable `knownHostsFile` path |
| 4 | Lazy connect: validate config at startup, defer the SSH handshake to first use | eager connect with a background reconnect supervisor |
| 5 | Drop the global `socks5Listen` added in the previous iteration | keep it alongside tunnel listeners |
| 6 | Tunnels live under `upstreams` as `type: ssh` | a separate top-level `tunnels:` map |

Decision 2 was reversed mid-brainstorm: an earlier draft delegated to
`~/.ssh/config`. Inlining makes the config self-contained — it works in Docker
and CI images that have no `~/.ssh` at all — and drops a dependency
(`github.com/kevinburke/ssh_config`) along with the target-resolution code.
The cost is that dockprox no longer inherits a user's existing SSH setup; a
laptop user must restate `host`/`user`/`keyFile` that `~/.ssh/config` already
knows.

Decision 3 is the security-relevant one: an unverified host key lets anyone who
can MITM the bastion read all tunneled traffic, including credentials for the
internal hosts being proxied to. So there is no opt-out. Decision 3a keeps
verification possible in both worlds: an inline `hostKey` fingerprint for
self-contained deploys, and `~/.ssh/known_hosts` (populated by a one-time
`ssh <host>` login) on a dev machine.

Decision 4's failure mode is deferred *network* errors only. Required fields and
key-file readability are checked at startup, so a typo or an unreadable key still
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

### `Target`

A plain struct built directly from the `config.Upstream` fields — no file
parsing, no alias resolution. `Port` defaults to 22 and `User` to the current OS
user when omitted; everything else comes from config verbatim.

A `Validate()` method is called for every tunnel at startup: it checks required
fields, that `keyFile` (when set) exists and parses as a private key, and that
`hostKey` (when set) is a well-formed fingerprint. Unreadable keys and typos
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

Auth methods are assembled from config, in this order:

1. `keyFile`, decrypted with `keyFilePassphrase` when that is set
   (`ssh.ParsePrivateKeyWithPassphrase`, else `ssh.ParsePrivateKey`).
2. Agent via `SSH_AUTH_SOCK` when `agent: true`
   (`golang.org/x/crypto/ssh/agent`).

At least one must be configured; validation rejects an `ssh` upstream with
neither. An encrypted `keyFile` with no `keyFilePassphrase` and no agent fails at
startup with an error naming the file, since the passphrase can never be supplied
later — dockprox does not prompt (a menubar daemon has no terminal).

`keyFilePassphrase` is plaintext-on-disk in the YAML, matching the precedent set
by the existing `auth.password` on SOCKS5/HTTP upstreams. Env-var interpolation
is out of scope (see below). Password auth is not supported (decision Q1=C).

### Host key verification

Strict, with two trust sources (decision 3a):

1. When `hostKey` is set, the presented key's fingerprint must equal it exactly.
   Accepted format is `SHA256:<base64>`, as printed by `ssh-keyscan`
   piped through `ssh-keygen -lf -`.
2. Otherwise, `golang.org/x/crypto/ssh/knownhosts` against
   `~/.ssh/known_hosts`.

Unknown or mismatched keys fail the connection. The error carries the presented
fingerprint and, in the fallback case, the `ssh <host>` remedy — in the pinned
case, the expected-vs-presented pair. No opt-out (decision 3).

A missing or unreadable `~/.ssh/known_hosts` when no `hostKey` is pinned is a
startup error, not a silent accept-anything.

### ProxyJump

Deferred. With `~/.ssh/config` parsing gone there is no `ProxyJump` directive to
inherit, and expressing a jump chain inline would mean a nested upstream
reference — a config-shape question worth its own design. Single-hop only in this
iteration; see Out of scope.

### Dependencies

- `golang.org/x/crypto` — `ssh`, `ssh/agent`, `ssh/knownhosts`

One new dependency, down from two: inlining the SSH target (decision 2) removes
the need for `github.com/kevinburke/ssh_config`. `.golangci.yaml` uses
`modules-download-mode: readonly`, so `make tidy` must run before lint.

## Config

```yaml
upstreams:
  jump:
    type: ssh
    host: bastion.example.com      # required: hostname or IP
    port: 22                       # optional, default 22
    user: deploy                   # optional, default current OS user
    keyFile: ~/.ssh/id_ed25519     # key auth; or agent: true, or both
    keyFilePassphrase: ""          # optional, for an encrypted keyFile
    agent: false                   # use SSH_AUTH_SOCK
    hostKey: "SHA256:abc123..."    # optional; falls back to ~/.ssh/known_hosts
    socks5Listen: 127.0.0.1:1080   # optional: expose a local SOCKS5 port

rules:
  - match: "*.internal.example.com"
    upstream: jump
```

`config.Upstream` gains `Host`, `Port`, `User`, `KeyFile`, `KeyFilePassphrase`,
`Agent`, `HostKey`, and `Socks5Listen` — all `omitempty` except as noted. A
`UpstreamSSH = "ssh"` constant joins the existing type constants, and the
`jsonschema:"enum=..."` tag on `Type` gains `enum=ssh`.

`~` in `keyFile` is expanded against the current user's home directory; a bare
relative path is resolved against the process working directory.

Note: `Port` as an `int` makes "unset" and `0` indistinguishable in Go's zero
value, which is fine here — `0` is not a valid port, so both mean "default to
22".

### Validation

Added to `Config.Validate` / `Upstream.validate`:

- `type: ssh` requires `host`.
- `type: ssh` requires at least one of `keyFile` or `agent: true`.
- `keyFile`, when set, must exist and parse as a private key —
  with `keyFilePassphrase` if given, without it otherwise. An encrypted key and
  no passphrase and no agent is an error.
- `hostKey`, when set, must be a well-formed `SHA256:<base64>` fingerprint.
- When `hostKey` is unset, `~/.ssh/known_hosts` must be readable.
- `port`, when set, must be 1–65535.
- The SSH-only fields (`host`, `port`, `user`, `keyFile`, `keyFilePassphrase`,
  `agent`, `hostKey`) are **rejected on non-`ssh` upstreams**, mirroring the
  `socks5Listen` rule below.
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
| `pkg/config` | Table-driven validation: ssh without `host`; ssh with neither `keyFile` nor `agent`; malformed `hostKey`; out-of-range `port`; ssh-only fields on an `http` upstream; `socks5Listen` on an `http` upstream; duplicate `socks5Listen`; unreferenced tunnel with no listener; valid tunnel. Plus the existing example-config test. |
| `pkg/sshclient` | `Target` defaults (port 22, current user) and `Validate`: generated unencrypted key accepted; encrypted key with correct passphrase accepted; encrypted key with no passphrase and no agent rejected; missing key file rejected; `~` expansion. Fingerprint parsing/comparison against a generated host key. No network. |
| `pkg/socks5server` | Existing Matcher-path tests keep passing; one new test for the fixed-`Dialer` path. |
| End-to-end | The test that proves the chain. |

The end-to-end test runs an in-process SSH server (`x/crypto/ssh` supports this)
with a generated host key, pins that key's fingerprint via `hostKey`, points a
tunnel at it, then drives a real SOCKS5 client through the listener to a local
echo server and asserts bytes round-trip. Pinning rather than a fixture
`known_hosts` keeps the test independent of `$HOME`. Two more cases on the same
harness: a deliberately wrong `hostKey` must fail the connection (proving
verification is not vacuous), and closing the SSH server's conn mid-test then
issuing another request covers the lazy-reconnect path.

Verification: `make check` (tidy + generate + lint + test.race + audit).
`make generate` regenerates `dockprox.schema.json`; the committed schema must
match.

## Docs

`docs/guide/usage.md` currently instructs the reader to run `ssh -D 1080 -N
jumphost` manually. This feature replaces that workflow, so the page is updated.
`dockprox.example.yaml` gains a documented `type: ssh` upstream and loses the
top-level `socks5Listen` block.

Because `hostKey` is new surface, the docs must show how to obtain a
fingerprint:

```sh
ssh-keyscan -t ed25519 bastion.example.com 2>/dev/null | ssh-keygen -lf - | awk '{print $2}'
```

The docs should also note that omitting `hostKey` falls back to
`~/.ssh/known_hosts`, and that a containerized dockprox almost certainly wants
the pinned form.

## Out of scope

- Eager connect with keepalives and backoff. Decision 4 chose lazy; the upgrade
  path is a supervisor goroutine calling `Client.Get` on a timer, and it is
  additive.
- Reading anything from `~/.ssh/config`. Reversed by decision 2. `known_hosts` is
  the one exception, and only as a fallback when `hostKey` is unset.
- `ProxyJump` / multi-hop chains. Needs a config shape for nested upstream
  references; own design.
- Password authentication (decision Q1=C).
- Passphrase prompting. `keyFilePassphrase` or an agent, or it fails at startup.
- Env-var interpolation for secrets (`keyFilePassphrase: ${VAR}`). A broader
  config-loader feature that would apply to the existing `auth.password` fields
  too.
- Surfacing per-tunnel state in the menubar UI.
- Local/remote port forwards (`-L` / `-R`). Only SOCKS5 (`-D`) is in scope.
