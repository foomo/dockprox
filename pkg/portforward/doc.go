// Package portforward implements a local TCP port forward, the equivalent
// of `ssh -L`: every accepted connection is dialed to one fixed target
// through an upstream.Dialer and spliced. The dialer is either fixed
// (Options.Dialer) or picked per connection by routing the target's host
// through the Matcher/Registry used by pkg/proxy. Clients need no proxy
// support at all.
package portforward
