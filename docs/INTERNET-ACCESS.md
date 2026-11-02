# Internet access: VCRX behind Caddy with a DNS-only name

How to let a few friends reach VCRX at `https://watch.example.com` with their own accounts, streaming straight from
your server. Nothing here turns itself on: every step that exposes the server is yours to run, in order, and the
rollback at the end undoes it in under a minute.

```
friend's browser ─► watch.example.com (Cloudflare DNS only, grey cloud) ─► your public IP
  ─► router: TCP 443 ─► server LAN_IP:3752 (Caddy, TLS) ─► 127.0.0.1:3750 (VCRX)
```

Placeholders: `watch.example.com` (your name), `example.com` (its Cloudflare zone), `LAN_IP` (the server's LAN
address), `PUBLIC_IP` (your home address). VCRX's port 3750 is never forwarded.

## 1. Cloudflare DNS

1. **Pick a name nothing else uses.** An existing name that already points at a Cloudflare Tunnel or a proxied
   record cannot be reused: those go through Cloudflare's proxy, which is exactly what this design avoids.
2. **Create the record:** DNS > Records > Add: type `A`, name `watch`, IPv4 `PUBLIC_IP`, **Proxy status: DNS only
   (grey cloud)**, TTL `5 min` (Auto is also 5 min for DNS-only records). Add no `AAAA` unless your router forwards
   IPv6 to the server as well.
3. **Optional, recommended:** a CAA record on the zone, `0 issue "letsencrypt.org"`, so no other authority issues a
   certificate for it. Check first that nothing else in the zone uses another CA.
4. **Verify from outside your LAN.** Many home routers answer every port-53 query themselves, so `dig` at home can
   report what the router thinks. Ask Cloudflare's DNS over HTTPS instead:
   ```bash
   curl -s -H 'accept: application/dns-json' 'https://cloudflare-dns.com/dns-query?name=watch.example.com&type=A'
   ```
   The answer must be `PUBLIC_IP`. A Cloudflare address (104.x, 172.64.x and so on) means the record is proxied:
   switch it to grey.

**DNS only versus proxied.** DNS only: Cloudflare just answers "where is watch.example.com" with your IP; video goes
straight from your server to the friend, and your IP is visible to anyone who looks the name up (that is
unavoidable with a direct connection). Proxied: every byte passes through Cloudflare, which hides the IP but puts
video through a CDN whose terms are written for web pages, adds a hop, and hands TLS to Cloudflare. This guide uses
DNS only.

No Cloudflare API token is needed: the certificate comes from the TLS-ALPN-01 challenge over the forwarded port
(section 3), so the server holds no DNS credential at all.

### If your public IP changes

Most fibre addresses are stable for months, but not guaranteed. Either run a DNS updater such as
[favonia/cloudflare-ddns](https://github.com/favonia/cloudflare-ddns) (pin its image by digest, `PROXIED=false`,
`DOMAINS=watch.example.com`, and a token with Zone > DNS > Edit on that one zone only), or use the router's dynamic
DNS if it supports Cloudflare. That token is the only credential this design ever needs, and only if the IP moves.

## 2. Caddy

`deploy/caddy/` holds a Dockerfile (the stock Caddy image, pinned by digest, plus the Caddyfile), a compose file and
the Caddyfile. What the Caddyfile does:

- HTTPS on `LAN_IP:3752` only, a Let's Encrypt certificate by TLS-ALPN-01 over the forwarded 443 (no port 80, no
  DNS token), automatic renewal.
- HSTS, `nosniff`, `X-Frame-Options`, `Referrer-Policy`, a closed `Permissions-Policy`; VCRX's own
  Content-Security-Policy passes through.
- Refuses `/api/v1/admin*`, `/api/v1/hooks/*` and `/api/v1/federation/*` from the internet. VCRX refuses admin from
  public addresses too; this is the second wall.
- Streams without buffering (`flush_interval -1`), no write timeout (films run for hours), Range requests pass
  through untouched. No WebSockets are needed.
- Replaces any `X-Forwarded-For` a client sends with the real client address, and drops `Forwarded` and
  `X-Real-IP`. VCRX reads only `X-Forwarded-For`, only from a trusted proxy, right to left.
- Rate limits: stock Caddy has none. VCRX limits sign-in (per address and per account), pairing and invites itself,
  and caps transcodes server-wide (`max_transcodes`).

Install on the server:

```bash
cd ~/docker/vcrx/deploy/caddy
cp caddy.env.example caddy.env && chmod 600 caddy.env   # fill in the four values
mkdir -p data config && chmod 700 data
docker compose build
docker compose run --rm caddy caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile
docker compose up -d
```

Caddy runs as an unprivileged user with every capability dropped, a read-only root, 256 MiB and 128 processes. It
uses host networking so it reaches VCRX on loopback and VCRX sees it as `127.0.0.1`.

## 3. Certificates

Let's Encrypt validates by connecting to `watch.example.com:443`, so **the certificate can only be issued once the
router forward (section 5) exists**. Started before that, Caddy logs failed attempts and retries with a growing
delay; that is harmless. Once the forward is on, watch `docker logs -f vcrx-caddy` for `certificate obtained
successfully` (usually under a minute; the first visitors in that minute may see a certificate warning). Renewal
happens about 30 days before expiry, over the same forward, so turning the forward off for weeks at a time lets the
certificate lapse; Caddy renews again as soon as it is back. Check expiry:

```bash
echo | openssl s_client -connect LAN_IP:3752 -servername watch.example.com 2>/dev/null | openssl x509 -noout -dates -issuer
```

Certificates live in `deploy/caddy/data`; losing it only means Caddy asks for a new one (mind Let's Encrypt's rate
limit of 5 duplicate certificates a week).

## 4. VCRX configuration

In `deploy/data/config/vcrx.yaml`:

```yaml
trusted_proxies: [127.0.0.1, "::1"]   # Caddy, and nothing else
allowed_hosts: [watch.example.com]    # VCRX answers 421 to any other name
```

Restart VCRX. Two checks fail safe if this is wrong: a proxy that is not listed makes every visitor "unknown" (no
LAN rights, no sign-in without a password, logged once as a warning), and a `trusted_proxies` of `0.0.0.0/0` stops
VCRX from starting.

Keep `admin_allow_remote` off. Keep `trusted_networks` to your own networks: anyone whose real address falls inside
them gets the profile picker or auto sign-in, and through Caddy the address is the friend's own public one, so they
never do.

## 5. Router (UniFi)

You make these changes yourself; nothing in this repository touches the router.

1. Settings > Routing > Port Forwarding > Create: name `vcrx-https`, WAN interface, **from: Any**, port `443`,
   forward IP `LAN_IP`, forward port `3752`, protocol **TCP**, logging on.
2. Do not forward 3750, 80, or any other port to the server. Leave your existing Plex forward exactly as it is.
3. Check that UniFi's own remote management and SSH are not exposed on the WAN (they are not by default).
4. Optional: Settings > Security > enable Region blocking or an IPS profile for the WAN; keep logging on.
5. Leave the firewall rules that separate the server from the rest of the LAN in place.

The forward is the on/off switch: disable it and the server is unreachable from the internet again.

## 6. Accounts for friends

One account each, never a shared one, and never an admin:

```bash
docker compose exec vcrx vcrx user add -username alex              # prompts for the password twice
docker compose exec vcrx vcrx user grant -username alex -libraries Movies
```

Passwords are argon2id hashes; give each friend a long random password over a private channel. In the web app
(Settings > Users, on the LAN), set each friend's policy: libraries, rating limits, whether they may transcode or
download. Friend accounts are not household members, so they never see the profile picker or other people's
profiles.

## 7. Library access

A friend sees only the libraries granted to them, everywhere: browsing, search, Home rows, artwork, subtitles and
every streaming route check the policy on every request, and playback sessions belong to the user who opened them.
Grant only what you mean to share. Accounts may play the original file as well as converted streams; to share
stream-only access, use an invited device instead.

## 8. Testing from outside

From a phone on mobile data (not your Wi-Fi):

1. `https://watch.example.com` loads with a valid padlock; sign-in asks for a password (no picker, no auto sign-in).
2. `https://watch.example.com/api/v1/server` shows `"auto_sign_in":false` and `"profile_picker":false`.
3. `https://watch.example.com/api/v1/admin/users` answers 403.
4. A film plays, seeks and resumes; a second device of the same account can play at the same time.
5. `http://PUBLIC_IP:3750/healthz` does not connect.
6. Settings > Activity on the LAN shows the phone's carrier address, not `127.0.0.1` or a LAN address.

## 9. Logging and monitoring

- Caddy: `docker logs vcrx-caddy` (JSON; Cookie and Authorization are redacted).
- VCRX: `docker logs vcrx`; failed sign-ins log `login failed` with the client address; a missing
  `trusted_proxies` entry logs `request has proxy headers from an address not in trusted_proxies`.
- Router: the port forward's log, and Insights for blocked traffic.
- Watch certificate expiry and `https://watch.example.com/healthz` from an uptime checker outside your LAN.

## 10. Backup and recovery

Back up `deploy/data/config` (the database, `vcrx.yaml`, the federation key) as for any VCRX install, plus
`deploy/caddy/caddy.env`. `deploy/caddy/data` is replaceable. Restoring is the README's restore procedure, then
`docker compose up -d` in `deploy/caddy`.

## 11. Rollback

Fastest first:

1. Disable the router's `vcrx-https` forward. The server is off the internet.
2. `cd deploy/caddy && docker compose down`.
3. Delete the `watch` record in Cloudflare.
4. Remove `allowed_hosts` and `trusted_proxies` from `vcrx.yaml` and restart VCRX.

Step 1 alone is a full rollback of the exposure; the rest is tidying up.
