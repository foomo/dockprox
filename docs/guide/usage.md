# Usage

## Quick start

Get your bastion's host key fingerprint:

```sh
ssh-keyscan -t ed25519 jumphost 2>/dev/null | ssh-keygen -lf - | awk '{print $2}'
```

Create `~/dockprox.yaml`:

```yaml
version: 1
listen: 127.0.0.1:8888
upstreams:
  jump:
    type: ssh
    host: jumphost
    keyFile: ~/.ssh/id_ed25519
    hostKey: "SHA256:..."        # from the command above
rules:
  - { match: "*.azurecr.io", upstream: jump }
  - { match: ghcr.io, upstream: jump }
```

Start dockprox — it opens the SSH connection itself, on first matched request:

```sh
dockprox serve --config ~/dockprox.yaml
```

Point your shell at it:

```sh
export HTTPS_PROXY=http://127.0.0.1:8888
export HTTP_PROXY=http://127.0.0.1:8888
```

## `az acr login` through a SOCKS5 jumphost

```yaml
upstreams:
  jump:
    type: ssh
    host: jumphost
    keyFile: ~/.ssh/id_ed25519
    hostKey: "SHA256:..."
rules:
  - { match: "*.azurecr.io",            upstream: jump }
  - { match: management.azure.com,      upstream: jump }
  - { match: login.microsoftonline.com, upstream: jump }
```

```sh
az acr login -n myreg                   # Azure CLI + ACR routed via jumphost
docker pull myreg.azurecr.io/app:tag    # docker daemon proxy set separately
```

Everything else stays direct.

## Local clusters on non-standard ports

A `type: forward` upstream ignores the requested target and always dials
`addr`. The hostname still travels in the TLS SNI and the `Host` header,
so an ingress controller keeps routing by name while the connection lands
on a different port.

This is how you reach several local k3d/kind clusters that share one
wildcard domain but cannot all bind `:443`:

```yaml
version: 1
listen: 127.0.0.1:1030
upstreams:
  cluster-a: { type: forward, addr: 127.0.0.1:10310 }
  cluster-b: { type: forward, addr: 127.0.0.1:10320 }
rules:
  - { match: "*.a.local.gd", upstream: cluster-a }
  - { match: "*.b.local.gd", upstream: cluster-b }
```

Point the browser's proxy at `127.0.0.1:1030` (e.g. a FoxyProxy pattern
for `*.local.gd`) and `https://app.a.local.gd` reaches cluster A's
ingress on port 10310 — no port in the URL, no `/etc/hosts` entries.

## Listeners

`listeners` opens local ports besides the HTTP proxy on `listen`. Each
has a `type`:

| `type`             | What it is                                                                  |
|--------------------|-----------------------------------------------------------------------------|
| `socks5` (default) | A SOCKS5 server, for tools that speak SOCKS5 rather than `HTTPS_PROXY`.    |
| `tcp`              | A plain port forward to `target`, like `ssh -L`. Clients need no proxy support. |

A listener either pins one upstream or routes by `rules`:

```yaml
listeners:
  - listen: 127.0.0.1:1080   # pinned: every connection → jump, rules ignored
    upstream: jump
  - listen: 127.0.0.1:1090   # no upstream: routes by rules, unmatched → direct

rules:                       # shared by the HTTP proxy and unpinned listeners
  - match: "*.internal.example.com"
    upstream: jump
```

An unpinned socks5 listener is the SOCKS5 twin of the HTTP proxy on
`listen`. Any upstream type can be pinned — e.g. an openconnect VPN for a
SOCKS5 port straight into it. In the menu bar app every listener is a row
you can start and stop.

### TCP forwards

A `tcp` listener forwards every connection to one `target`, through the
pinned upstream — or, without `upstream`, through whatever `rules` match
the target's host:

```yaml
listeners:
  - type: tcp
    listen: 127.0.0.1:5432
    target: db.internal.example.com:5432   # resolved on the far side
    upstream: jump
```

```sh
psql -h 127.0.0.1 -p 5432 ...   # no proxy settings needed
```

## SSH tunnels

A `type: ssh` upstream is an SSH connection dockprox owns. It connects
lazily — on the first request that matches a rule pointing at it — and
reconnects transparently after a sleep/wake or a dropped link.

### Authentication

Set `keyFile`, `identityAgent`, or both. There is no password auth and no
passphrase prompt: an encrypted `keyFile` needs `keyFilePassphrase` or a
usable agent, otherwise dockprox fails at startup.

`identityAgent` takes OpenSSH's values:

| Value | Meaning |
|-------|---------|
| `SSH_AUTH_SOCK` | Read the socket path from the `$SSH_AUTH_SOCK` env var |
| a path (`/run/agent.sock`, `~/.1password/agent.sock`) | Use that socket directly |
| omitted | No agent auth |

### Host key verification

Strict, with no opt-out — an unverified host key would let anyone who can
MITM the bastion read every byte you tunnel through it, including
credentials for the internal hosts behind it.

Two trust sources:

- **Pinned** — set `hostKey` to the `SHA256:...` fingerprint from
  `ssh-keyscan -t ed25519 HOST 2>/dev/null | ssh-keygen -lf - | awk '{print $2}'`.
- **`~/.ssh/known_hosts`** — the fallback when `hostKey` is unset. Populate
  it with a one-time `ssh HOST` login. dockprox fails at startup if the
  file is unreadable and no `hostKey` is pinned.

A containerised dockprox almost certainly wants the pinned form: an image
usually has no `~/.ssh` at all.

### Reachability

An ssh upstream must be used somehow: dockprox rejects one that no rule,
[listener](#listeners) or `via` references.

The former `socks5Listen` field on ssh upstreams is gone; a config that
still sets it fails to load. Replace it with a pinned listener:

```yaml
listeners:
  - listen: 127.0.0.1:1080
    upstream: jump
```

### Reaching the bastion through a VPN

Set `via` to the name of another (non-ssh) upstream to dial the bastion
through it instead of directly — typically an [OpenConnect VPN](#openconnect-vpn):

```yaml
upstreams:
  jump:
    type: ssh
    host: jumphost.internal
    identityAgent: SSH_AUTH_SOCK
    via: vpn
```

The SSH handshake timeout is 45s instead of 15s when the bastion is
reached through `via`: the first connection through a freshly started VPN
can be slow.

### Not supported

`ProxyJump` / ssh-through-ssh chains, remote port forwards (`-R`; for
`-L` use a [tcp listener](#tcp-forwards)), password authentication, and reading `~/.ssh/config` (`known_hosts`
is the one exception, and only as the host-key fallback).

## OpenConnect VPN

A `type: openconnect` upstream replaces a VPN client app (F5, AnyConnect,
GlobalProtect, Pulse, Fortinet) without root. dockprox runs

```sh
openconnect --script-tun --script "ocproxy -D <port>" ...
```

so the VPN ends in ocproxy's userspace TCP/IP stack and is reachable only
through dockprox: no tun device, no system routes, no `sudo`, and the rest
of the machine keeps its normal network.

```sh
brew install openconnect ocproxy
```

```yaml
upstreams:
  vpn:
    type: openconnect
    server: https://vpn.example.com
    protocol: f5                    # default f5
    auth:
      username: me
      password: secret              # sent on stdin, never on the command line
    serverCert: pin-sha256:...      # optional
```

Use it as a rule target, a listener's `upstream`, or the `via` of an ssh
upstream. The VPN
starts on first use and waits up to 90s for the tunnel, so an MFA push
approval fits. If openconnect exits, the next request starts it again —
and asks for another approval. Stopping dockprox logs the VPN session out.

dockprox runs openconnect with `--non-inter`: logins that need more than
username, password and an out-of-band approval (SAML in a browser, extra
form fields, a client certificate) are not supported. Try the exact
command by hand first:

```sh
echo "$PASSWORD" | openconnect --protocol=f5 --user me --passwd-on-stdin --non-inter \
  --script-tun --script 'ocproxy -D 11080' https://vpn.example.com
curl --socks5-hostname 127.0.0.1:11080 http://intranet.example.com
```

### Kernel tun mode

ocproxy's userspace TCP stack runs TCP over TCP when the VPN has no DTLS,
and under a bulk upload it freezes for a minute at a time. The kernel's
TCP stack over a real tun degrades gracefully instead. `tun: true` trades
the rootless setup for that: dockprox runs

```sh
sudo -n openconnect --user me ...
```

so openconnect brings up a kernel tun, and matched hosts are dialed
directly; the kernel routes them through the tun. Unlike ocproxy mode,
the routes are not contained in dockprox: openconnect's vpnc-script
applies the VPN's split routes system-wide while the tunnel is up, and
removes them when it logs out. DNS stays contained: dockprox runs
vpnc-script without the VPN's nameservers, so the system resolver is left
alone, and resolves matched hosts with those nameservers itself. Public
names keep resolving publicly, as in ocproxy mode.

```yaml
upstreams:
  vpn:
    type: openconnect
    server: https://vpn.example.com
    tun: true
    auth:
      username: me
      password: secret
```

`sudo -n` never prompts, so openconnect and `kill` (which stops it) need
a passwordless sudoers entry. Install it once, either from the terminal:

```sh
dockprox tun install
```

or from the menu bar: Settings > Install VPN sudoers. Both ask for your
password once and write `/etc/sudoers.d/dockprox`, validated by `visudo`:

```
me ALL=(root) NOPASSWD: /opt/homebrew/bin/openconnect, /bin/kill
```

Delete that file to undo. Note that the `kill` entry is unrestricted: with
it, your user can signal any root process without a password.

ocproxy is not needed in this mode. The VPN password still goes to
openconnect on stdin, not on the command line.
