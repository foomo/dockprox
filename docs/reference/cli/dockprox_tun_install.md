## dockprox tun install

Install the sudoers drop-in that tun mode needs

### Synopsis

Writes /etc/sudoers.d/dockprox so that openconnect (to start the VPN) kill (to stop it) and, on macOS, route limited to deleting a host route (to clear a stale VPN gateway route) run as root without a password for the current user. sudo asks for your password once. Delete the file to undo.

```
dockprox tun install [flags]
```

### Options

```
  -h, --help   help for install
```

### SEE ALSO

* [dockprox tun](dockprox_tun.md)	 - Set up kernel tun mode for openconnect upstreams

