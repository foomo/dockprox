package config

// Version is the config format version this build accepts. Every config
// file must declare it as `version: 1`.
const Version = 1

// Config is the top-level dockprox configuration.
type Config struct {
	Version   int                 `json:"version"           yaml:"version"           jsonschema:"enum=1,description=config format version"`
	Listen    string              `json:"listen"            yaml:"listen"            jsonschema:"description=local proxy listen address (host:port)"`
	LogLevel  string              `json:"logLevel"          yaml:"logLevel"          jsonschema:"enum=debug,enum=info,enum=warn,enum=error,default=info"`
	LogFile   string              `json:"logFile,omitempty" yaml:"logFile,omitempty" jsonschema:"description=log file path (default: OS cache dir)"`
	Upstreams map[string]Upstream `json:"upstreams"         yaml:"upstreams"`
	Rules     []Rule              `json:"rules"             yaml:"rules"`
	Listeners []Listener          `json:"listeners,omitempty" yaml:"listeners,omitempty"`
	Chrome    *Chrome             `json:"chrome,omitempty"  yaml:"chrome,omitempty"`
}

// Listener is a local port. A socks5 listener (the default) is a SOCKS5
// server; a tcp listener forwards every connection to Target, like
// `ssh -L`. With Upstream set it is pinned: every connection goes through
// that upstream and rules are ignored. Without, the requested host (socks5)
// or Target's host (tcp) is routed by rules, and unmatched hosts dial
// direct.
type Listener struct {
	Type     string `json:"type,omitempty"     yaml:"type,omitempty"     jsonschema:"enum=socks5,enum=tcp,default=socks5"`
	Listen   string `json:"listen"             yaml:"listen"             jsonschema:"description=local listen address (host:port)"`
	Upstream string `json:"upstream,omitempty" yaml:"upstream,omitempty" jsonschema:"description=pin every connection to this upstream; omit to route by rules"`
	Target   string `json:"target,omitempty"   yaml:"target,omitempty"   jsonschema:"description=tcp: host:port every connection is forwarded to"`
}

// Chrome configures the isolated Chrome instance the menu bar app can
// launch against the local proxy.
type Chrome struct {
	App   string   `json:"app,omitempty"   yaml:"app,omitempty"   jsonschema:"description=app name / bundle ID / .app path as accepted by open -a (default: Google Chrome)"`
	Flags []string `json:"flags,omitempty" yaml:"flags,omitempty" jsonschema:"description=extra command line flags appended after the proxy and profile flags"`
}

// Upstream defines a named proxy upstream that rules can reference.
type Upstream struct {
	Type string        `json:"type"           yaml:"type"           jsonschema:"enum=socks5,enum=http,enum=direct,enum=ssh,enum=forward,enum=openconnect"`
	Addr string        `json:"addr,omitempty" yaml:"addr,omitempty" jsonschema:"description=host:port for socks5; fixed dial target for forward"`
	URL  string        `json:"url,omitempty"  yaml:"url,omitempty"  jsonschema:"description=URL for http upstreams"`
	DNS  string        `json:"dns,omitempty"  yaml:"dns,omitempty"  jsonschema:"enum=local,enum=remote,default=local"`
	Auth *UpstreamAuth `json:"auth,omitempty" yaml:"auth,omitempty"`
	TLS  *UpstreamTLS  `json:"tls,omitempty"  yaml:"tls,omitempty"`

	// SSH tunnel fields; valid only when Type is "ssh".
	Host              string `json:"host,omitempty"              yaml:"host,omitempty"              jsonschema:"description=ssh: bastion hostname or IP"`
	Port              int    `json:"port,omitempty"              yaml:"port,omitempty"              jsonschema:"description=ssh: port (default 22)"`
	User              string `json:"user,omitempty"              yaml:"user,omitempty"              jsonschema:"description=ssh: login user (default: current OS user)"`
	KeyFile           string `json:"keyFile,omitempty"           yaml:"keyFile,omitempty"           jsonschema:"description=ssh: private key path"`
	KeyFilePassphrase string `json:"keyFilePassphrase,omitempty" yaml:"keyFilePassphrase,omitempty" jsonschema:"description=ssh: passphrase for an encrypted keyFile; literal or secret reference (op://... | keychain://service/account | cmd:...)"`
	IdentityAgent     string `json:"identityAgent,omitempty"     yaml:"identityAgent,omitempty"     jsonschema:"description=ssh: SSH_AUTH_SOCK or an agent socket path"`
	HostKey           string `json:"hostKey,omitempty"           yaml:"hostKey,omitempty"           jsonschema:"description=ssh: pinned SHA256:... host key fingerprint; falls back to ~/.ssh/known_hosts"`
	Via               string `json:"via,omitempty"               yaml:"via,omitempty"               jsonschema:"description=ssh: name of a non-ssh upstream to dial the bastion through"`

	// OpenConnect VPN fields; valid only when Type is "openconnect". Login
	// credentials come from Auth.
	Server     string `json:"server,omitempty"     yaml:"server,omitempty"     jsonschema:"description=openconnect: VPN gateway URL"`
	Protocol   string `json:"protocol,omitempty"   yaml:"protocol,omitempty"   jsonschema:"enum=f5,enum=anyconnect,enum=nc,enum=gp,enum=pulse,enum=fortinet,enum=array,default=f5"`
	ServerCert string `json:"serverCert,omitempty" yaml:"serverCert,omitempty" jsonschema:"description=openconnect: pinned server certificate (pin-sha256:...), passed as --servercert"`
}

// UpstreamAuth carries optional username/password for SOCKS5 (RFC 1929) or
// HTTP Basic auth on the upstream CONNECT request, or the openconnect
// login. Either may be a secret reference (see package secret).
type UpstreamAuth struct {
	Username string `json:"username" yaml:"username" jsonschema:"description=literal or secret reference (op://... | keychain://service/account | cmd:...)"`
	Password string `json:"password" yaml:"password" jsonschema:"description=literal or secret reference (op://... | keychain://service/account | cmd:...)"`
}

// UpstreamTLS configures the TLS dial to an HTTPS upstream proxy. It does
// not affect tunnelled client TLS (which is end-to-end and opaque).
type UpstreamTLS struct {
	InsecureSkipVerify bool   `json:"insecureSkipVerify,omitempty" yaml:"insecureSkipVerify,omitempty"`
	CAFile             string `json:"caFile,omitempty"             yaml:"caFile,omitempty"`
}

// Rule pairs a host pattern with the name of an upstream defined above.
type Rule struct {
	Match    string `json:"match"    yaml:"match"    jsonschema:"description=exact host or *.suffix wildcard"`
	Upstream string `json:"upstream" yaml:"upstream"`
}
