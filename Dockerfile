FROM alpine:3.24.2@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6

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
