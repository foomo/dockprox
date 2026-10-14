// Package secret resolves credential values from config that may reference
// an external secret store instead of holding the secret itself:
//
//   - op://vault/item/field: 1Password, via `op read` (desktop app unlock)
//   - keychain://service/account: macOS Keychain, via `security`
//   - cmd:<shell>: stdout of a /bin/sh command (pass, bw, vault, ...); only
//     with [WithCmd], as it runs arbitrary shell
//
// Any other value is a literal and returned unchanged.
package secret
