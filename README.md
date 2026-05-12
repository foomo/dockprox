[![Go Report Card](https://goreportcard.com/badge/github.com/foomo/dockprox?style=flat-square)](https://goreportcard.com/report/github.com/foomo/dockprox)
[![GoDoc](https://img.shields.io/badge/GoDoc-✓-informational.svg?style=flat-square&logo=go)](https://godoc.org/github.com/foomo/dockprox)
[![GitHub Downloads](https://img.shields.io/github/downloads/foomo/dockprox/total.svg?style=flat-square&logo=github)](https://github.com/foomo/dockprox/releases)
[![Docker Pulls](https://img.shields.io/docker/pulls/foomo/dockprox.svg?style=flat-square&logo=docker)](https://hub.docker.com/r/foomo/dockprox)
[![GitHub Stars](https://img.shields.io/github/stars/foomo/dockprox.svg?style=flat-square&logo=github)](https://github.com/foomo/dockprox)

<p align="center">
  <img alt="dockprox" src="docs/public/logo.png" width="400" height="400"/>
</p>

# dockprox

> Inverse HTTP(S) proxy with SOCKS5 support — direct by default, route only what you choose.

## Overview

`dockprox` is a local HTTP(S) proxy that dials destinations directly by default. Only hosts matched by a rule in your config are forwarded through a named upstream — SOCKS5, HTTP CONNECT, or explicit `direct`. It bridges `HTTPS_PROXY`-style clients (which speak HTTP CONNECT) to SOCKS5 upstreams, so tools like `docker pull` and `az acr login` transparently get a SOCKS5 path without needing native support.

## Why

The standard `HTTPS_PROXY` + `NO_PROXY` contract is _"proxy everything; exclude via `NO_PROXY`"_ — opt-out, brittle for long allow-lists. `dockprox` inverts it: opt-in routing per host pattern. Public internet stays direct; only the registries you list (e.g. `*.azurecr.io`, internal Harbor, `ghcr.io`) go through your SOCKS5 jumphost.

See [docs/guide/why.md](docs/guide/why.md) for the full rationale.

## Quick start

Create `dockprox.yaml`:

```yaml
listen: 127.0.0.1:3128
logLevel: info
upstreams:
  jumphost:
    type: socks5
    addr: 127.0.0.1:1080
rules:
  - match: "*.azurecr.io"
    upstream: jumphost
```

Run:

```shell
dockprox serve --config dockprox.yaml
```

Point your client at it:

```shell
export HTTPS_PROXY=http://127.0.0.1:3128
docker pull myregistry.azurecr.io/image:tag
```

Flags can also override or supply config inline:

```shell
dockprox serve \
  --listen 127.0.0.1:3128 \
  --upstream jumphost=socks5://127.0.0.1:1080 \
  --rule '*.azurecr.io=jumphost'
```

## Configuration

Top-level keys (`dockprox.schema.json`):

| Key         | Description                                          |
|-------------|------------------------------------------------------|
| `listen`    | Local proxy bind address (`host:port`).              |
| `logLevel`  | `debug` \| `info` \| `warn` \| `error`.              |
| `upstreams` | Map of named upstream proxies.                       |
| `rules`     | Ordered list of `match` → `upstream` mappings.       |

Upstream `type` values:

- `socks5` — `addr: host:port`, optional `auth`, `tls`, `dns: local|remote`.
- `http` — HTTP CONNECT proxy, `url: http(s)://...`.
- `direct` — explicit passthrough.

Rule `match`: exact host (`ghcr.io`) or `*.suffix` wildcard (`*.azurecr.io`).

Full reference: [docs/guide/configuration.md](docs/guide/configuration.md) · JSON Schema: [`dockprox.schema.json`](dockprox.schema.json).

## Use cases

- **Azure Container Registry** — route `*.azurecr.io` through a corporate SOCKS5 jumphost; everything else direct.
- **GitHub Container Registry** — send `ghcr.io` through SOCKS5 only when on a restricted network.
- **Private Harbor / internal registries** — proxy internal hosts while keeping Docker Hub and public mirrors direct.

## Documentation

- [Installation](docs/guide/installation.md)
- [Usage](docs/guide/usage.md)
- [Configuration](docs/guide/configuration.md)
- [Why dockprox](docs/guide/why.md)

## Installation

<details>
<summary><b>Homebrew</b> (macOS / Linux)</summary>

```shell
brew install foomo/tap/dockprox
```

See the [foomo/homebrew-tap](https://github.com/foomo/homebrew-tap) repository.

</details>

<details>
<summary><b>Docker</b></summary>

```shell
docker run --rm foomo/dockprox:latest scan
```

Multi-arch images (`amd64`, `arm64`) are published to [Docker Hub](https://hub.docker.com/r/foomo/dockprox).

</details>

<details>
<summary><b>mise</b></summary>

```shell
mise use github:foomo/dockprox
```

or run directly:

```shell
mise x github:foomo/dockprox -- scan
```

See [mise.jdx.dev](https://mise.jdx.dev).

</details>

<details>
<summary><b>Binary release</b></summary>

Download the archive for your OS/arch from the [releases page](https://github.com/foomo/dockprox/releases) and extract `dockprox` into your `$PATH`.

</details>

<details>
<summary><b>go install</b></summary>

```shell
go install github.com/foomo/dockprox/cmd/dockprox@latest
```

Requires Go 1.26+.

</details>

## How to Contribute

Contributions are welcome! Please read the [contributing guide](CONTRIBUTING.md).

![Contributors](https://contributors-table.vercel.app/image?repo=foomo/dockprox&width=50&columns=15)

## License

Distributed under MIT License, please see license file within the code for more details.

_Made with ♥ [foomo](https://www.foomo.org) by [bestbytes](https://www.bestbytes.com)_
