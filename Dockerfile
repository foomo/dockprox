FROM debian:stable-slim@sha256:eb593cf2c358cacef45ca0a424bbc7d30cfa3466265fc2662b9466a0ca6ba1c5

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates openconnect ocproxy \
    && rm -rf /var/lib/apt/lists/*

RUN useradd -m -u 1001 dockprox

COPY dockprox /usr/bin/

USER dockprox
WORKDIR /home/dockprox

ENTRYPOINT ["dockprox"]
