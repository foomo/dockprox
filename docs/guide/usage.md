# Usage

## Quick start

Get your bastion's host key fingerprint:

```sh
ssh-keyscan -t ed25519 jumphost 2>/dev/null | ssh-keygen -lf - | awk '{print $2}'
```

Create `~/dockprox.yaml`:

```yaml
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

### Exposing a SOCKS5 port

Set `socks5Listen` to give a tunnel its own local SOCKS5 listener, for
tools that speak SOCKS5 directly rather than through `HTTPS_PROXY`:

```yaml
upstreams:
  jump:
    type: ssh
    host: jumphost
    keyFile: ~/.ssh/id_ed25519
    hostKey: "SHA256:..."
    socks5Listen: 127.0.0.1:1080
```

Every connection that listener accepts goes through that one bastion,
regardless of host — the rules do not apply to it.

An ssh upstream must be reachable somehow: dockprox rejects one that no
rule references and that sets no `socks5Listen`.

### Reaching the bastion through a VPN

Set `via` to the name of another (non-ssh) upstream to dial the bastion
through it instead of directly — typically an [OpenConnect VPN](#openconnect-vpn):

```yaml
upstreams:
  jump:
    type: ssh
    host: jumphost.internal
    identityAgent: SSH_AUTH_SOCK
    socks5Listen: 127.0.0.1:1080
    via: vpn
```

The SSH handshake timeout is 45s instead of 15s when the bastion is
reached through `via`: the first connection through a freshly started VPN
can be slow.

### Not supported

`ProxyJump` / ssh-through-ssh chains, local and remote port forwards (`-L` /
`-R`), password authentication, and reading `~/.ssh/config` (`known_hosts`
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

Use it as a rule target, or as the `via` of an ssh upstream. The VPN
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
