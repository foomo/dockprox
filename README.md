[![Go Report Card](https://goreportcard.com/badge/github.com/foomo/dockprox?style=flat-square)](https://goreportcard.com/report/github.com/foomo/dockprox)
[![GoDoc](https://img.shields.io/badge/GoDoc-✓-informational.svg?style=flat-square&logo=go)](https://godoc.org/github.com/foomo/dockprox)
[![GitHub Downloads](https://img.shields.io/github/downloads/foomo/dockprox/total.svg?style=flat-square&logo=github)](https://github.com/foomo/dockprox/releases)
[![Docker Pulls](https://img.shields.io/docker/pulls/foomo/dockprox.svg?style=flat-square&logo=docker)](https://hub.docker.com/r/foomo/dockprox)
[![GitHub Stars](https://img.shields.io/github/stars/foomo/dockprox.svg?style=flat-square&logo=github)](https://github.com/foomo/dockprox)

<p align="center">
  <img alt="dockprox" src="docs/public/logo.png" width="400" height="400"/>
</p>

# dockprox

> Reverse docker proxy with socks5 support


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
