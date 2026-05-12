## dockprox serve

Run the proxy server

```
dockprox serve [flags]
```

### Options

```
      --config string          YAML config file or '-' for stdin
  -h, --help                   help for serve
      --listen string          listen address (overrides config)
      --log-level string       log level: debug|info|warn|error
      --rule stringArray       PATTERN=UPSTREAM (repeatable)
      --upstream stringArray   NAME=URL (repeatable)
```

### SEE ALSO

* [dockprox](dockprox.md)	 - Inverse Docker proxy with SOCKS5 support

