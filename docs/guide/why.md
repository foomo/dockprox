# Why dockprox

Docker has no native SOCKS5 support, and the standard `HTTPS_PROXY` /
`HTTP_PROXY` contract is _"proxy everything by default; exclude via
`NO_PROXY`"_. That shape is wrong for the common case: developers want
the public internet to stay direct and only route a small set of
registries (e.g. `*.azurecr.io`, internal Harbor, ghcr) through a SOCKS5
jumphost.

`dockprox` inverts the model.

- **Default:** dial destinations directly.
- **Matched domains:** forward through a named upstream — SOCKS5 or HTTP
  CONNECT.

It also bridges `HTTPS_PROXY`-style clients (which speak HTTP CONNECT) to
upstreams that speak SOCKS5, so tools like `az acr login` and `docker pull`
transparently get a SOCKS5 path without needing native support.
