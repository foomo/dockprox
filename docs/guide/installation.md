# Installation

## Homebrew

```sh
brew install foomo/tap/dockprox
```

## Docker

```sh
docker run --rm foomo/dockprox:latest version
```

## mise

```sh
mise use github:foomo/dockprox
```

## go install

```sh
go install github.com/foomo/dockprox/cmd/dockprox@latest
```

Requires Go 1.26+.

## macOS menu bar app

```sh
brew install --cask foomo/tap/dockprox-menubar
```

Or build it yourself from this repo — see [Menu bar app](./menubar.md).

## Optional dependencies

dockprox itself has none. Some features shell out to external tools, which
are looked up in `$PATH`, then `/opt/homebrew/bin` and `/usr/local/bin`
(the menu bar app gets a minimal `PATH` when launched from Finder). A
config that needs a missing tool fails at startup, naming the install
command.

| Feature | Tools | Install |
|---|---|---|
| [`openconnect` upstream](./usage.md#openconnect-vpn) | `openconnect`, `ocproxy` | `brew install openconnect ocproxy` |
| [`op://` secrets](./configuration.md#secrets) | 1Password CLI `op` | `brew install --cask 1password-cli`, then enable *Settings → Developer → Integrate with 1Password CLI* in the desktop app |
| [`keychain://` secrets](./configuration.md#secrets) | `security` | ships with macOS |

The Docker image includes `openconnect` and `ocproxy`.
