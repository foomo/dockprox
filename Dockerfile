FROM debian:stable-slim

# Debian, not Alpine: Alpine does not package ocproxy, which openconnect
# upstreams need.
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates openconnect ocproxy \
    && rm -rf /var/lib/apt/lists/*

RUN useradd -m -u 1001 dockprox

COPY dockprox /usr/bin/

USER dockprox
WORKDIR /home/dockprox

ENTRYPOINT ["dockprox"]
