# Stock Caddy: TLS-ALPN-01 needs no extra module. Pinned by digest; bump the
# version and digest together.
FROM caddy:2.11.7-alpine@sha256:e70b164da81ef698b3ad19af799ea6086ce6d9e48ed4079eb9fe26f6fbf14ee5
# The stock binary carries the cap_net_bind_service file capability, and a
# container with no-new-privileges and every capability dropped refuses to
# exec it ("operation not permitted"). HTTPS_PORT is above 1024, so drop it.
RUN setcap -r /usr/bin/caddy && [ -z "$(getcap /usr/bin/caddy)" ]
COPY Caddyfile /etc/caddy/Caddyfile
