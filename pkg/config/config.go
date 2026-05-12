package config

// Config is the top-level dockprox configuration.
type Config struct {
	Listen    string              `json:"listen"    yaml:"listen"    jsonschema:"description=local proxy listen address (host:port)"`
	LogLevel  string              `json:"logLevel"  yaml:"logLevel"  jsonschema:"enum=debug,enum=info,enum=warn,enum=error,default=info"`
	Upstreams map[string]Upstream `json:"upstreams" yaml:"upstreams"`
	Rules     []Rule              `json:"rules"     yaml:"rules"`
}

// Upstream defines a named proxy upstream that rules can reference.
type Upstream struct {
	Type string        `json:"type"           yaml:"type"           jsonschema:"enum=socks5,enum=http,enum=direct"`
	Addr string        `json:"addr,omitempty" yaml:"addr,omitempty" jsonschema:"description=host:port for socks5"`
	URL  string        `json:"url,omitempty"  yaml:"url,omitempty"  jsonschema:"description=URL for http upstreams"`
	DNS  string        `json:"dns,omitempty"  yaml:"dns,omitempty"  jsonschema:"enum=local,enum=remote,default=local"`
	Auth *UpstreamAuth `json:"auth,omitempty" yaml:"auth,omitempty"`
	TLS  *UpstreamTLS  `json:"tls,omitempty"  yaml:"tls,omitempty"`
}

// UpstreamAuth carries optional username/password for SOCKS5 (RFC 1929) or
// HTTP Basic auth on the upstream CONNECT request.
type UpstreamAuth struct {
	Username string `json:"username" yaml:"username"`
	Password string `json:"password" yaml:"password"`
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
