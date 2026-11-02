# Caddy with the Cloudflare DNS provider, for DNS-01 certificates. The stock
# image does not include it. Both images are pinned by digest; bump the
# versions and digests together.
FROM caddy:2.11.7-builder-alpine@sha256:95a5e7e2692b5eb9545aadc5b90a39f6f509bee59c9508a493f83ee902eaef63 AS build
RUN xcaddy build v2.11.7 --with github.com/caddy-dns/cloudflare@v0.2.4

FROM caddy:2.11.7-alpine@sha256:e70b164da81ef698b3ad19af799ea6086ce6d9e48ed4079eb9fe26f6fbf14ee5
COPY --from=build /usr/bin/caddy /usr/bin/caddy
# The build fails here, not at certificate time, if the module went missing.
RUN caddy list-modules | grep -qx 'dns.providers.cloudflare'
COPY Caddyfile /etc/caddy/Caddyfile
