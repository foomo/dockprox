# Configuration

`dockprox serve` reads YAML from a file (`--config path`), stdin
(`--config -`), or relies on flags + defaults.

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/foomo/dockprox/main/dockprox.schema.json
version: 1
listen: 127.0.0.1:8888
logLevel: info

upstreams:
  jump:
    type: socks5        # socks5 | http | direct | ssh | forward | openconnect
    addr: 127.0.0.1:1080
    dns: remote         # remote (socks5h) | local
    auth:
      username: u
      password: p
    tls:                # only relevant for https:// upstreams
      insecureSkipVerify: false
      caFile: /etc/dockprox/ca.pem

  bastion:
    type: ssh
    host: bastion.example.com   # required
    port: 22                    # optional, default 22
    user: deploy                # optional, default: current OS user
    keyFile: ~/.ssh/id_ed25519  # key auth; or identityAgent, or both
    keyFilePassphrase: ""       # optional, for an encrypted keyFile; accepts a secret reference
    identityAgent: SSH_AUTH_SOCK # agent auth: this sentinel, or a socket path
    hostKey: "SHA256:..."       # pinned fingerprint; falls back to ~/.ssh/known_hosts
    via: vpn                    # optional: dial the bastion through another upstream

  vpn:
    type: openconnect
    server: https://vpn.example.com # required
    protocol: f5                # f5 (default) | anyconnect | nc | gp | pulse | fortinet | array
    auth:                       # required: username; password sent on stdin
      username: u
      password: op://Work/VPN/password # secret reference, see below
    serverCert: pin-sha256:...  # optional

  cluster-a:
    type: forward
    addr: 127.0.0.1:10310       # required; every matched host dials this

listeners:                    # local ports besides the HTTP proxy
  - listen: 127.0.0.1:1080    # type: socks5 (default)
    upstream: bastion         # pinned; omit to route by rules
  - listen: 127.0.0.1:1090
  - type: tcp                 # port forward, like ssh -L
    listen: 127.0.0.1:5432
    target: db.internal:5432
    upstream: bastion

rules:
  - match: "*.azurecr.io"     # exact host or *.suffix wildcard
    upstream: jump
```

## Secrets

`auth.username`, `auth.password` and `keyFilePassphrase` take a literal or a
secret reference, resolved on first use and cached until dockprox exits:

| Value | Resolved by |
|---|---|
| `op://Vault/Item/field` | 1Password CLI: `op read` (unlocks via the desktop app) |
| `keychain://service/account` | macOS Keychain: `security find-generic-password -w` |
| `cmd:<shell>` | stdout of `/bin/sh -c <shell>`, e.g. `cmd:pass show vpn` |

The tool a reference needs must be installed, see
[Optional dependencies](./installation.md#optional-dependencies); a missing
one fails at startup. A trailing newline is stripped. `/opt/homebrew/bin` and `/usr/local/bin` are
added to `PATH`, so `op` is found when the menu bar app is launched from
Finder. A literal that starts with a prefix above can be written as
`cmd:printf %s '...'`.

## Flags

```
--config PATH|-              # file path or '-' for stdin
--listen ADDR                # default 127.0.0.1:8888
--log-level LEVEL            # debug | info | warn | error
--upstream NAME=URL          # repeatable; socks5://h:p, http://h:p, forward://h:p, direct
--rule PATTERN=UPSTREAM      # repeatable
```

## Precedence

`flags > env (DOCKPROX_*) > stdin/file > defaults`

## See also

- [Listeners](./usage.md#listeners)
- [SSH tunnels](./usage.md#ssh-tunnels)
- [OpenConnect VPN](./usage.md#openconnect-vpn)
