# Menu bar app (macOS)

A native menu bar (tray) app ships as a separate `dockprox-menubar` binary. It runs a `dockprox` proxy in-process and
exposes Start / Stop / Restart / Open-Chrome / Reveal-in-Finder (logs, config) / Quit from the system
tray.

::: info Platform The menu bar app is **macOS only** (Wails-backed, requires cgo + macOS SDK). It is not part of the
standard `dockprox` release archives — build it locally on a Mac.
:::

## Install

```sh
brew install --cask foomo/tap/dockprox-menubar
```

::: warning Unsigned app The app is currently ad-hoc signed, not notarized. On first launch, macOS Gatekeeper will block
it — open **System Settings → Privacy & Security** and click **Open Anyway**, then confirm in the dialog that appears.
:::

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

The proxy auto-starts when the app launches. The tray icon reflects the running / stopped state, and dims briefly while
a restart is in progress.

## Flags

```sh
dockprox-menubar [--config PATH]
```

| Flag       | Description                                          |
|------------|------------------------------------------------------|
| `--config` | YAML config file. Default: auto-resolve (see below). |

## Config resolution

When `--config` is omitted, the app searches in this order:

1. `$XDG_CONFIG_HOME/dockprox/config.yaml` (if `XDG_CONFIG_HOME` is set)
2. `~/.config/dockprox/config.yaml` (when `XDG_CONFIG_HOME` is unset)
3. `~/.dockprox.yaml`

If none of these exist, a default config is written:

- to `$XDG_CONFIG_HOME/dockprox/config.yaml` when `XDG_CONFIG_HOME` is set,
- otherwise to `~/.dockprox.yaml`.

Parent directories are created as needed.

## Logging

Logs are written to `logFile` if set in the config, otherwise to the OS cache dir:
`~/Library/Caches/org.foomo.dockprox/dockprox.log`.

## Tray actions

| Action | Effect |
|--------|--------|
| **Version** (top item)      | Shows the running app version. Click to open the GitHub releases page.                                                                                                                            |
| *(listener rows)*           | Grouped with the proxy address: one row per listener, e.g. `◉ 127.0.0.1:1080 → bastion` (SOCKS5, pinned), `◉ 127.0.0.1:1090` (SOCKS5, routes by rules) or `◉ 127.0.0.1:5432 → db.internal:5432 via bastion` (TCP forward). Click a listening one (`◉`) to stop it, or a stopped one (`○`) to start it, independently of the main proxy. |
| *(upstream rows)*           | Next block: one row per `ssh` and `openconnect` upstream, e.g. `◉ bastion (ssh): connected`. Click a disconnected one (`○`, `⚠︎`) to connect it, or a connected / connecting one to disconnect — see [Upstream connection](#upstream-connection). |
| *(forward rows)*            | Same block: one row per `forward` upstream with its fixed `addr`, e.g. `◉ staging (forward): 127.0.0.1:8443`. Read-only — see [Forward status](#forward-status). |
| **Settings → Chrome Sandbox** | Launch an isolated Chrome instance pointed at the proxy (see [Isolated Chrome](#isolated-chrome)). Only enabled while the proxy is running. |
| **Settings → Start / Stop** | Start the in-process proxy with the resolved config, or stop it. |
| **Settings → Restart** | Stop, reload the config from disk, and start again. Disabled while the proxy is not running. |
| **Settings → Autoconnect** | On (default): an `ssh` or `openconnect` upstream connects on demand when a request, listener or `via` needs it. Off: requests only use connections that are already up; opening one takes a click on its row. Saved in `~/Library/Application Support/dockprox/settings.json`. |
| **Settings → Start at Login** | Toggle launching the app automatically at login — needed since dockprox must be running for a system-wide proxy setup to reach anything. |
| **Settings → Reveal logs / config in Finder** | Open the log file (see [Logging](#logging)) or the resolved config file in Finder. |
| **Quit**                    | Stop the proxy and exit the app.                                                                                                                                                                  |

Edit the resolved config file with any editor and use **Restart** to apply changes — there is no live-reload.

Listener and upstream rows are disabled while the main proxy is not running.

```
┌────────────────────────────────────────┐
│ 🏄‍♂️ v1.x.y                              │
├────────────────────────────────────────┤
│ ◉ 127.0.0.1:8888                       │
│ ◉ 127.0.0.1:1080 → bastion             │
│ ◉ 127.0.0.1:1090                       │
├────────────────────────────────────────┤
│ ◉ bastion (ssh): connected             │
│ ◉ vpn (openconnect): connected         │
│ ◉ cluster-a (forward): 127.0.0.1:10310 │
├────────────────────────────────────────┤┌────────────────────────────────────────┐
│ ⚙︎ Settings                             ││ ▶︎ Chrome Sandbox                       │
├────────────────────────────────────────┤├────────────────────────────────────────┤
│ ⏏︎ Quit                                 ││ ⏹︎ Stop                                 │
└────────────────────────────────────────┘│ ↺ Restart                              │
                                          ├────────────────────────────────────────┤
                                          │ ☑︎ Autoconnect                          │
                                          │ □ Start at Login                       │
                                          ├────────────────────────────────────────┤
                                          │ ↗ Reveal logs in Finder                │
                                          │ ↗ Reveal config in Finder              │
                                          └────────────────────────────────────────┘
```

## Upstream connection

Each `ssh` and `openconnect` upstream has a row with the state of its connection:

| Glyph | Meaning                                                                                       |
|-------|-----------------------------------------------------------------------------------------------|
| `○`   | Disconnected: never connected yet, or disconnected from the menu. Click to connect.           |
| `⊙`   | Connecting. For `openconnect`, approve the MFA prompt on your device. Click to abort.         |
| `☎︎`   | Waiting for you to approve the SSH key in your agent (see [1Password SSH agent](#1password-ssh-agent)). Click to abort. |
| `⚠︎`   | The last attempt failed, or the connection dropped; the reason is in the log. Click to retry. |
| `◉`   | Connected. Click to disconnect.                                                               |

With **Autoconnect** on (the default), a connection also opens on its own
when a proxied request, a listener or a `via` needs it — unless you
disconnected it from the menu: then it stays down, and requests through
it fail, until you click it again. Starting a listener pinned to an ssh
upstream connects it eagerly.

With **Autoconnect** off, nothing connects on its own: requests use a
connection only if it is already up, so background traffic never sends
you an unexpected MFA or agent prompt. Clicking an ssh upstream brings up
its `via` VPN too.

The SSH TCP connect and handshake are bounded by 15s (45s through `via`).
While the agent holds a sign request (`☎︎`), the bound is 60s instead, so
there is time to approve it. Requests that queue behind a failed attempt
get its error rather than each retrying in turn.

### 1Password SSH agent

1Password [suppresses its approval prompt](https://developer.1password.com/docs/ssh/agent/authorization/) when the
requesting app is not in the foreground. A menu bar app never is on its own, so dockprox brings itself to the front
just before it asks the agent to sign. The prompt then pops up. Afterwards, dockprox hides itself again, and focus
returns to the app you were using. This happens on every new SSH connection, so you may see a short focus flicker even
when 1Password already remembers the approval.

This needs dockprox to run as `Dockprox.app`: macOS only grants the activation through Launch Services, which needs the
app bundle.

If macOS refuses the activation, 1Password shows an indicator dot on its menu bar icon instead of the prompt. While the
upstream shows `☎︎`, open the 1Password menu and select **SSH request waiting** to approve it.

1Password remembers the approval for dockprox until it locks or quits, or for the time set under **Settings → Developer
→ Remember key approval**. Checking **Approve for all applications** in the prompt covers dockprox too.

## Forward status

Each `forward` upstream gets a row showing its configured `addr` and whether that endpoint currently accepts a TCP
connection:

| Glyph | Meaning                                                        |
|-------|----------------------------------------------------------------|
| `◉`   | The endpoint accepted a connection.                            |
| `○`   | The dial failed — nothing is listening, or it timed out.       |
| `?`   | Not probed yet (shown until the first probe of the session).   |

Unlike tunnels, forwards have no listener of their own to start or stop, so the rows are informational only.

Endpoints are probed when you open the menu, not on a background timer — so there is no periodic dial traffic while the
menu is closed. The menu opens immediately with the previous verdicts and updates in place once the probes land, each
capped at 500ms. A successful dial only proves something is listening on that address; it does not check that the backend
behind it is healthy.

## Responsiveness

Menu clicks are dispatched on the macOS UI thread, so any action that touches the filesystem, binds a listener, or waits
on a shutdown runs on a background goroutine instead — the menu bar stays responsive while it works. The tray icon dims
and the affected actions are disabled until the action completes, which also prevents a second click from racing the
first.

Shutdown waits are bounded: **Stop** and stopping a tunnel wait at most 5s for the listener's accept loop to return.
Cancelling already closes the listener, so this deadline only matters if that goroutine is wedged — exceeding it logs a
warning and gives up the wait rather than freezing the app, and the proxy is stopped either way. One deadline covers the
proxy and all tunnels together, so N tunnels cannot stack N timeouts.

**Restart** deliberately proceeds even if its Stop phase times out: the listener is already closed, so Start can rebind,
and bailing out would leave the proxy down.

## Isolated Chrome

**Settings → Chrome Sandbox** launches Chrome against the proxy's listen address without touching your own Chrome profile:

```sh
open -n -a "Google Chrome" --args \
  --proxy-server=http://<listen addr> \
  --proxy-bypass-list='<-loopback>' \
  --user-data-dir=~/Library/Application\ Support/dockprox/chrome \
  --no-first-run --no-default-browser-check
```

`-n` forces a separate instance — without it macOS just activates an already-running Chrome and discards the flags, so
the proxy and the isolated profile would be silently ignored.

The separate `--user-data-dir` means this instance keeps its own cookies, logins and extensions, and runs alongside your
normal Chrome. The profile directory persists between launches; delete it to reset the instance.

`--proxy-bypass-list=<-loopback>` removes Chrome's built-in loopback bypass, so rules matching hosts that resolve to
`127.0.0.1` (e.g. `*.local.gd`) still go through the proxy.

Both the browser and extra flags are configurable via an optional `chrome` section in the config:

```yaml
chrome:
  # App name, bundle ID, or .app path — anything `open -a` accepts.
  # Default: Google Chrome.
  app: Google Chrome
  # Extra flags, appended after the flags above (so they win).
  flags:
    - --incognito
    - --window-size=1280,800
```

::: info Why `open` and not the binary directly Executing the binary inside `Google Chrome.app` makes macOS gate the
call behind the **App Management** permission, prompting *"dockprox needs permission to update or delete other
applications"*. Nothing is being modified — that is just the generic wording macOS uses for any write, rename, or exec
against a path inside an app bundle. Because the app is only ad-hoc signed, the grant does not stick and the prompt
returns after every rebuild. Going through `open` hands the launch to LaunchServices, so dockprox never touches the
bundle and no permission is needed.
:::

Changes take effect after **Restart**, which reloads the config from disk.

::: warning Incognito and the profile
`--incognito` still uses the `--user-data-dir` above, but discards session state on exit — so logins do not persist
between launches.
:::

## See also

- [Configuration](./configuration.md)
