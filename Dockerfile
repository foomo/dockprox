FROM alpine:latest

RUN apk --no-cache add ca-certificates

RUN adduser -D -u 1001 -g 1001 dockprox

COPY dockprox /usr/bin/

USER dockprox
WORKDIR /home/dockprox

ENTRYPOINT ["dockprox"]
