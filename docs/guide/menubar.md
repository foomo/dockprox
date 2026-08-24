# Menu bar app (macOS)

A native menu bar (tray) app ships as a separate `dockprox-menubar` binary. It runs a `dockprox` proxy in-process and exposes Start / Stop / Restart / Reveal-config-in-Finder / Quit from the system tray.

::: info Platform
The menu bar app is **macOS only** (Wails-backed, requires cgo + macOS SDK). It is not part of the standard `dockprox` release archives — build it locally on a Mac.
:::

## Install

```sh
brew install --cask foomo/tap/dockprox-menubar
```

## Build & run

From this repo on macOS:

```sh
make build.menubar
bin/dockprox-menubar
```

Or run from source:

```sh
go run -tags=safe ./cmd/dockprox-menubar
```

The proxy auto-starts when the app launches. The tray icon reflects the running / stopped state.

## Flags

```sh
dockprox-menubar [--config PATH]
```

| Flag       | Description                                                  |
|------------|--------------------------------------------------------------|
| `--config` | YAML config file. Default: auto-resolve (see below).         |

## Config resolution

When `--config` is omitted, the app searches in this order:

1. `$XDG_CONFIG_HOME/dockprox/config.yaml` (if `XDG_CONFIG_HOME` is set)
2. `~/.config/dockprox/config.yaml` (when `XDG_CONFIG_HOME` is unset)
3. `~/.dockprox.yaml`

If none of these exist, a default config is written:

- to `$XDG_CONFIG_HOME/dockprox/config.yaml` when `XDG_CONFIG_HOME` is set,
- otherwise to `~/.dockprox.yaml`.

Parent directories are created as needed.

## Tray actions

| Action                    | Effect                                                                 |
|---------------------------|------------------------------------------------------------------------|
| **Start**                 | Start the in-process proxy with the resolved config.                   |
| **Stop**                  | Stop the running proxy.                                                |
| **Restart**               | Stop, reload the config from disk, and start again.                    |
| **Reveal config in Finder** | Open the resolved config file in Finder.                             |
| **Quit**                  | Stop the proxy and exit the app.                                       |

Edit the resolved config file with any editor and use **Restart** to apply changes — there is no live-reload.

## See also

- [Configuration](./configuration.md)
