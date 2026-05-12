# Usage

## Quick start

Create `~/dockprox.yaml`:

```yaml
listen: 127.0.0.1:8888
upstreams:
  jump:
    type: socks5
    addr: 127.0.0.1:1080
    dns: remote
rules:
  - { match: "*.azurecr.io", upstream: jump }
  - { match: ghcr.io, upstream: jump }
```

Open a SOCKS5 tunnel and start dockprox:

```sh
ssh -D 1080 -N jumphost &
dockprox serve --config ~/dockprox.yaml
```

Point your shell at it:

```sh
export HTTPS_PROXY=http://127.0.0.1:8888
export HTTP_PROXY=http://127.0.0.1:8888
```

## `az acr login` through a SOCKS5 jumphost

```yaml
upstreams:
  jump:
    type: socks5
    addr: 127.0.0.1:1080
    dns: remote
rules:
  - { match: "*.azurecr.io",            upstream: jump }
  - { match: management.azure.com,      upstream: jump }
  - { match: login.microsoftonline.com, upstream: jump }
```

```sh
az acr login -n myreg                   # Azure CLI + ACR routed via jumphost
docker pull myreg.azurecr.io/app:tag    # docker daemon proxy set separately
```

Everything else stays direct.
