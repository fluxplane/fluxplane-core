# fluxplane/fluxplane-go: minimal runtime base for Fluxplane Go apps.
#
# This image contains no Fluxplane source — it is just the runtime userland
# that every app needs: CA certificates, timezone data, and a non-root user.
# Apps (slack-bot, etc.) FROM this image and add their statically-linked
# binary on top.
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S -g 10001 fluxplane && \
    adduser  -S -u 10001 -G fluxplane -H -h /home/fluxplane fluxplane && \
    mkdir -p /home/fluxplane /etc/fluxplane && \
    chown -R fluxplane:fluxplane /home/fluxplane /etc/fluxplane

ENV TZ=UTC \
    HOME=/home/fluxplane \
    XDG_CONFIG_HOME=/home/fluxplane/.config \
    XDG_DATA_HOME=/home/fluxplane/.local/share

USER fluxplane
WORKDIR /home/fluxplane

LABEL org.opencontainers.image.title="fluxplane-go" \
      org.opencontainers.image.source="https://github.com/fluxplane/fluxplane-core" \
      org.opencontainers.image.description="Runtime base image for Fluxplane Go apps."
