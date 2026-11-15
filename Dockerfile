FROM debian:stable-slim@sha256:fbfb4059d2127120aa05b6e6064523d2102552f0cf0026ddf0701c12f1f8f3e0

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates openconnect ocproxy \
    && rm -rf /var/lib/apt/lists/*

RUN useradd -m -u 1001 dockprox

COPY dockprox /usr/bin/

USER dockprox
WORKDIR /home/dockprox

ENTRYPOINT ["dockprox"]
