// Command dockprox is an inverse HTTP(S) proxy with SOCKS5 support.
//
// By default it dials destinations directly. Only hosts matched by a rule
// in the configuration file are routed through a named upstream proxy
// (SOCKS5 or HTTP CONNECT). See https://github.com/foomo/dockprox.
//
// # Usage
//
//	dockprox serve [flags]
//	dockprox version
//
// The serve command runs the proxy. Its flags are:
//
//	--config PATH        YAML config file, or "-" to read it from stdin
//	--listen ADDR        listen address, overriding the config
//	--log-level LEVEL    debug, info, warn or error
//	--upstream NAME=URL  define an upstream (repeatable)
//	--rule PATTERN=NAME  route hosts matching PATTERN via upstream NAME (repeatable)
package main
