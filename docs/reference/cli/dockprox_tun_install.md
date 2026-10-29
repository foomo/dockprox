## dockprox tun install

Install the sudoers drop-in that tun mode needs

### Synopsis

Writes /etc/sudoers.d/dockprox so that openconnect (to start the VPN) and kill (to stop it) run as root without a password for the current user. sudo asks for your password once. Delete the file to undo.

```
dockprox tun install [flags]
```

### Options

```
  -h, --help   help for install
```

### SEE ALSO

* [dockprox tun](dockprox_tun.md)	 - Set up kernel tun mode for openconnect upstreams

