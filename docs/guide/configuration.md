# Configuration

`dockprox serve` reads YAML from a file (`--config path`), stdin
(`--config -`), or relies on flags + defaults.

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/foomo/dockprox/main/dockprox.schema.json
listen: 127.0.0.1:8888
logLevel: info

upstreams:
  jump:
    type: socks5        # socks5 | http | direct
    addr: 127.0.0.1:1080
    dns: remote         # remote (socks5h) | local
    auth:
      username: u
      password: p
    tls:                # only relevant for https:// upstreams
      insecureSkipVerify: false
      caFile: /etc/dockprox/ca.pem

rules:
  - match: "*.azurecr.io"     # exact host or *.suffix wildcard
    upstream: jump
```

## Flags

```
--config PATH|-              # file path or '-' for stdin
--listen ADDR                # default 127.0.0.1:8888
--log-level LEVEL            # debug | info | warn | error
--upstream NAME=URL          # repeatable; socks5://h:p, http://h:p, direct
--rule PATTERN=UPSTREAM      # repeatable
```

## Precedence

`flags > env (DOCKPROX_*) > stdin/file > defaults`
