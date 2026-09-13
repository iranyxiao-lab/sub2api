# Edge and HTTP Ingress Security

Sub2API supports long-lived SSE and WebSocket requests. Protect the request
ingress without imposing a response `WriteTimeout`: a write deadline would
terminate healthy long generations and streams.

## Application defaults

- `server.max_header_bytes: 65536` limits HTTP/1 request headers to 64 KiB;
  Go maps it to the corresponding HTTP/2 header-list limit.
- `server.read_header_timeout: 10` bounds slow-header attacks. It does not
  limit request processing or response streaming.
- `server.max_request_body_size: 268435456` is the absolute 256 MiB safety net.
- `gateway.max_body_size: 268435456` remains available to multimodal, Gemini,
  image, video, and batch-image endpoints.
- `gateway.text_max_body_size: 33554432` limits the known pure-text
  `/embeddings` and `/alpha/search` endpoints to 32 MiB.
- H2C defaults to 50 concurrent streams per connection, a 2 MiB connection
  upload window, and a 512 KiB stream upload window.
- Invalid credential abuse is limited in process by trusted client IP (IPv6
  `/64`): 120 failures per 60 seconds followed by a 60-second block. This is a
  per-instance safety net; multi-instance enforcement still belongs at the
  load balancer, CDN, or WAF.

Do not add a single application-wide request semaphore: an SSE request may
legitimately occupy it for many minutes. Apply connection and unauthenticated
request controls at the edge; authenticated user/API-key concurrency remains
the application's responsibility.

## Trusted client IPs

`security.trust_forwarded_ip_for_api_key_acl` is enabled by default for upgrade
compatibility. While enabled, raw forwarding headers take over client-IP
resolution for logs and security-sensitive paths. Custom headers from
`security.forwarded_client_ip_headers` are checked in configured order before
the built-in `CF-Connecting-IP`, `X-Real-IP`, and `X-Forwarded-For` fallback.
Header names are case-insensitive, normalized when loaded, de-duplicated, and
limited to 16 unique valid HTTP field names. Header values must contain IP
literals; comma-separated values are supported, invalid entries are skipped,
and public addresses are preferred over private fallback addresses.

The list can be supplied in YAML or with the comma-separated environment
variable `SECURITY_FORWARDED_CLIENT_IP_HEADERS`; an explicitly empty environment
value clears YAML values. It is also editable from the admin security settings
and updates at runtime without a restart. A request snapshots the switch and
header list together, so one request cannot mix old and new settings. Custom
headers are ignored completely when the switch is disabled. In that mode Gin's
`server.trusted_proxies` chain is authoritative: configure only the exact
CIDR/IP addresses that connect directly to Sub2API. An explicit empty list
trusts no forwarded client IPs.

On the first upgrade to this mode, a legacy `false` value is changed to `true`
only when `server.trusted_proxies` was not explicitly configured; explicit
proxy policies remain in secure mode. New installations persist the configured
custom header list during database initialization. Existing installations
backfill a missing database value from the YAML configuration. A hidden
migration marker prevents later administrator changes from being overwritten.
If settings cannot be read or the persisted custom-header list is malformed,
the process fails closed to trusted-proxy mode with no custom headers. If a
migration write fails, the computed mode remains active for the current process
and startup records a warning.

Compatibility takeover accepts forwarded headers without validating the direct
peer, including any configured custom header. Protect the origin from direct
access while it is enabled. A CDN deployment must firewall the origin so only
the CDN or load balancer can reach it, and that proxy must overwrite every
trusted client-IP header rather than append an untrusted client value.

Example for a proxy on the same host:

```yaml
server:
  trusted_proxies:
    - 127.0.0.1/32
    - ::1/128
```

## Nginx baseline

Define shared zones in the `http` block. Tune rates to measured legitimate
traffic; the values below are conservative starting points, not universal
capacity targets.

```nginx
limit_conn_zone $binary_remote_addr zone=sub2api_conn:20m;
limit_req_zone  $binary_remote_addr zone=sub2api_auth:20m rate=5r/s;
limit_req_zone  $binary_remote_addr zone=sub2api_api:40m rate=30r/s;
map $http_upgrade $connection_upgrade {
    default upgrade;
    ''      close;
}

server {
    listen 443 ssl http2;
    server_name api.example.com;

    client_header_timeout 10s;
    client_max_body_size 256m;
    large_client_header_buffers 4 16k;
    limit_conn sub2api_conn 40;

    location ~ ^/(auth|api/auth)/ {
        limit_req zone=sub2api_auth burst=10 nodelay;
        proxy_pass http://127.0.0.1:8080;
    }

    location ~ ^/(v1/)?(embeddings|alpha/search)$ {
        client_max_body_size 32m;
        limit_req zone=sub2api_api burst=60 nodelay;
        proxy_pass http://127.0.0.1:8080;
    }

    location / {
        limit_req zone=sub2api_api burst=60 nodelay;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
        proxy_buffering off;
        proxy_request_buffering off;
        proxy_read_timeout 1800s;
        proxy_send_timeout 1800s;
        proxy_pass http://127.0.0.1:8080;
    }
}
```

If Nginx gzip is enabled in the `http` block, keep `text/event-stream` out of
`gzip_types` and do not use `gzip_types *` for Sub2API. The
`proxy_buffering off` setting above prevents proxy buffering, but it does not
disable the gzip response filter. Use an explicit list for ordinary responses:

```nginx
gzip on;
gzip_types text/plain text/css application/json application/javascript application/xml image/svg+xml;
```

If a shared global configuration cannot exclude SSE by content type, set
`gzip off;` in the locations serving streaming API routes. This leaves gzip
available for the web UI and static assets.

Do not use an incoming `$http_x_forwarded_for` value unless Nginx real-IP
processing is restricted to explicit trusted proxy CIDRs.

## Caddy and CDN

The bundled `deploy/Caddyfile` sets a 64 KiB header limit, a 10-second header
timeout, a 256 MiB absolute body limit, and overwrites forwarded addresses from
the TCP peer. It is therefore a direct-to-Caddy baseline. Do not use its
`{remote_host}` forwarding lines unchanged behind a CDN: all clients would be
attributed to a CDN egress address, collapsing rejection aggregation and the
invalid-auth limiter onto unrelated users.

The bundled Caddy configuration leaves `flush_interval` unset so Caddy can
automatically flush `text/event-stream` responses while still propagating
client cancellation upstream. Do not set it globally: positive values can add
streaming latency, while Caddy 2.6.2's special `-1` mode also causes
reverse-proxied requests to continue after clients disconnect. The
configuration uses an explicit response content-type list for compression. Do
not replace that list with `text/*` or the shorthand `encode gzip zstd`: both
match `text/event-stream` and can buffer SSE until the response ends. Keep
streaming responses uncompressed while retaining compression for the web UI,
JSON, and static assets.

For a CDN deployment, first firewall the origin so only current CDN egress
CIDRs can connect. Then configure those exact ranges as Caddy trusted proxies
and derive upstream headers from Caddy's parsed `{client_ip}`. For example:

```caddyfile
{
	servers {
		trusted_proxies static 192.0.2.0/24 2001:db8:1234::/48
		trusted_proxies_strict
		client_ip_headers CF-Connecting-IP X-Forwarded-For
	}
}

api.example.com {
	reverse_proxy 127.0.0.1:8080 {
		header_up X-Real-IP {client_ip}
		header_up X-Forwarded-For {client_ip}
	}
}
```

Replace the documentation ranges with the CDN's published, automatically
maintained egress ranges. `CF-Connecting-IP` is safe here only because direct
origin access is blocked and Caddy trusts only those TCP peers. Configure
Sub2API `server.trusted_proxies` with the Caddy address/private subnet so the
application accepts only Caddy's rewritten headers.

Caddy core does not provide a general request-rate limiter; use a trusted
CDN/WAF, a supported rate-limit module, or host firewall controls.

At a CDN/WAF, configure connection limits, header/body limits, bot challenges,
and per-IP/ASN rates before traffic reaches the origin. Allow origin ingress
only from CDN egress CIDRs or a private load balancer. Keep the application port
off the public Internet.

## DDoS boundary

Application checks reduce amplification after a connection reaches Go. They
cannot absorb volumetric attacks, TLS floods, bandwidth saturation, or a large
distributed source set. Those require upstream network capacity, CDN/WAF
filtering, provider firewall rules, and origin isolation. Avoid high-cardinality
metrics or per-request database security logs during rejection storms.

## heytoken.net Cloudflare origin isolation

The production rollout has two explicit modes:

1. **Proxied origin:** Cloudflare connects to public Caddy on TCP 80/443. The
   host firewall permits only Cloudflare's published IPv4 and IPv6 ranges.
2. **Tunnel-only origin:** cloudflared connects outbound, Caddy listens only
   on 127.0.0.1:18081, and public TCP 80/443 is denied for every source.

The first mode is an immediate containment measure and the rollback path. The
second mode is the steady state. A single connector has several edge
connections and systemd restart recovery, but it is not host-level high
availability.

Repository-managed files are under deploy/cloudflare/:

| File | Purpose |
| --- | --- |
| origin-guard.sh | Validate, cache, and atomically apply Cloudflare nftables ranges |
| sub2api-origin-guard.* | Restore the selected mode at boot and refresh every six hours |
| Caddyfile.cloudflare | Public Cloudflare origin plus loopback canary origin |
| Caddyfile.tunnel | Final loopback-only origin |
| configure-trusted-proxy.sh | Persist the unique Docker gateway as an exact /32 |
| install-cloudflared*.sh | Install cloudflared and its token without argv/env exposure |
| switch-caddy-mode.sh | Validate, back up, reload, health-check, and auto-restore Caddy |
| rollback-to-proxied-origin.sh | Restore the cached Cloudflare allowlist |

### Required preflight record

Before either phase, record these values in the private change record, not in
the repository:

- current proxied root A/AAAA records and TTL;
- sha256sum /etc/caddy/Caddyfile;
- sha256sum /opt/sub2api/docker-compose.yml;
- current immutable application image tag or digest;
- docker compose service state and application health;
- timestamp and restore result of the latest application/PostgreSQL backup;
- Tunnel ID after the Tunnel is created.

Do not print docker compose config; interpolation can expose secrets.

### Phase one: Cloudflare-only public origin

Install the repository files on the origin, then run:

~~~sh
cd /path/to/repository/deploy/cloudflare
sudo sh ./install-origin-guard.sh
sudo sh ./switch-caddy-mode.sh cloudflare
sudo sh ./configure-trusted-proxy.sh
cd /opt/sub2api
sudo docker compose -f docker-compose.yml up -d --no-deps --wait --wait-timeout 120 sub2api
~~~

configure-trusted-proxy.sh inspects the one running sub2api container. It
aborts unless all attached networks yield one unique, valid IPv4 gateway. It
then atomically writes SERVER_TRUSTED_PROXIES=<gateway>/32 to the mode-600
production .env; it never prints the rest of that file.

In the admin security settings, explicitly save both values and read them back:

~~~json
{
  "api_key_acl_trust_forwarded_ip": false,
  "forwarded_client_ip_headers": []
}
~~~

With compatibility takeover disabled, Caddy overwrites X-Real-IP and
X-Forwarded-For from Cloudflare's CF-Connecting-IP, while the application
accepts those rewritten headers only from the exact Docker gateway. Rate
limits, session binding, audit logs, and API-key IP ACLs therefore share the
same trusted-proxy chain.

The nftables script owns only inet sub2api_edge and TCP destination ports 80
and 443. It does not alter SSH, Docker, PostgreSQL, or Redis rules. Downloads
are fixed to Cloudflare's official ips-v4 and ips-v6 endpoints in the systemd
unit. Empty, malformed, duplicate, undersized, oversized, failed downloads, or
failed nft syntax checks leave the previous rules and cache active. The timer
runs every six hours with up to 30 minutes of randomized delay.

Verify from an external host, not from the origin itself:

~~~sh
curl --fail --show-error https://heytoken.net/health
curl --resolve heytoken.net:443:ORIGIN_IPV4 https://heytoken.net/health
curl -6 --resolve heytoken.net:443:ORIGIN_IPV6 https://heytoken.net/health
~~~

The domain request must pass; both direct-origin requests must time out or be
rejected. Also send a normal request with a deliberately forged
CF-Connecting-IP through Cloudflare and confirm the application records the
actual visitor address, not the supplied value.

### Phase two: remotely managed Tunnel

Create a remotely managed Tunnel named heytoken-origin in Cloudflare. Add a
temporary canary public hostname with:

- service: http://127.0.0.1:18081;
- HTTP Host Header: heytoken.net;
- connector transport: HTTP/2 over TCP 7844, forced with the documented
  `TUNNEL_TRANSPORT_PROTOCOL=http2` environment variable.

The phase-one Caddyfile already exposes the same application policy on
127.0.0.1:18081, so the canary can run while the root hostname continues to
use the proxied A record. The site address is written as `http://:18081` with
an explicit `bind 127.0.0.1`; this keeps the configuration compatible with the
production Caddy 2.6.2 parser while still preventing a public listener. Caddy
also fixes the upstream Host to `heytoken.net`.

Install cloudflared >= 2025.4.0 from Cloudflare's APT repository:

~~~sh
cd /path/to/repository/deploy/cloudflare
sudo sh ./install-cloudflared.sh
sudo sh ./install-cloudflared-token.sh < /secure/temporary/heytoken.token
sudo systemctl enable --now cloudflared
sudo systemctl --no-pager status cloudflared
curl --fail --silent http://127.0.0.1:20241/metrics >/dev/null
~~~

The temporary token file must be mode 0600 and deleted after installation. The
installed token is /etc/cloudflared/heytoken.token, owned by the cloudflared
service account with mode 0400. Never put it in a command-line argument,
Compose environment, shell history, repository, support bundle, or log.
The systemd command deliberately omits a `--protocol` argument because
cloudflared 2026.7.3 no longer exposes that CLI option. The service instead
sets the documented `TUNNEL_TRANSPORT_PROTOCOL=http2` environment variable.
This production-specific override avoids a failure mode observed with automatic
selection: three QUIC connections remained healthy while one IPv6 QUIC
connection repeatedly failed its control stream and never triggered a global
HTTP/2 fallback. TCP connectivity checks and a temporary connector must show
four stable HTTP/2 connections before enabling the override. To return to
automatic selection after the network path is repaired, remove the Environment
line, reload systemd, and restart only cloudflared.

Test the canary hostname for /health, login and 2FA, the admin UI, /v1/models,
one controlled SSE request, WebSocket upgrade, and a bounded upload. Confirm
the application still records the real visitor IP. Restart both Caddy and
cloudflared once during canary validation and confirm automatic recovery.

After canary approval, replace the root proxied A record with the Tunnel public
hostname route. Keep the old A value in the private rollback record. Observe
at least two previous DNS TTLs and 30 additional minutes while checking health,
5xx rate, latency, SSE disconnects, uploads, and connector metrics.

Finalize only after the observation window:

~~~sh
sudo sh /path/to/repository/deploy/cloudflare/switch-caddy-mode.sh tunnel
sudo /usr/local/sbin/sub2api-origin-guard deny-all
sudo systemctl disable --now sub2api-origin-guard.timer
~~~

Leave sub2api-origin-guard.service enabled. Its boot-time service-refresh
command records the selected mode and restores the unconditional 80/443 denial
after reboot without downloading or applying a Cloudflare allowlist. The
disabled timer and cached lists remain available for rollback.

### Tunnel token rotation and failure

For rotation, generate or rotate the token in Cloudflare, immediately install
the new value through install-cloudflared-token.sh standard input, restart
cloudflared, and verify active edge connections plus canary/root health. Treat
the rotation as a short reconnect window and never log either token.

For a connector-only incident, inspect redacted cloudflared journal output,
local Caddy health, DNS resolution, and the loopback metrics endpoint. Do not
repeatedly restart a failing connector.

If `cloudflared_tunnel_ha_connections` remains below four and the journal shows
one QUIC connection repeatedly failing while TCP 7844 passes the startup
connectivity checks, keep the HTTP/2 override in place. This changes only the
connector-to-Cloudflare transport; browser-to-Cloudflare HTTP/2, HTTP/3, SSE,
and WebSocket behavior is unaffected.

For a full rollback, use this order:

1. Restore the old proxied root A/AAAA record in Cloudflare.
2. Run rollback-to-proxied-origin.sh to restore the cached Cloudflare
   allowlist and periodic updates.
3. Run switch-caddy-mode.sh cloudflare.
4. Verify the domain through Cloudflare and direct-origin rejection.
5. Only then stop or disable cloudflared.

Never remove the nftables table or open 80/443 to the world as a temporary
rollback step.

When the origin IP changes in Tunnel mode, no public DNS change is needed.
Update the private rollback record and provider firewall. If rollback remains
required, pre-stage the new proxied A/AAAA value while keeping the Cloudflare
allowlist active on the new host.

### Cloudflare edge controls

Apply edge controls after canary validation:

- SSL/TLS mode **Full (strict)**, TLS 1.3 enabled, minimum TLS 1.2;
- **Always Use HTTPS** and Certificate Transparency monitoring enabled;
- DNSSEC enabled, with the Cloudflare DS record installed at the registrar and
  the signed chain verified externally;
- HSTS initially emitted by the repository Caddy templates as
  `max-age=86400`, without includeSubDomains or preload. The Cloudflare Free
  dashboard currently exposes one month as its smallest non-zero HSTS value,
  so it cannot represent the safer one-day rollout. After seven stable days,
  remove the Caddy HSTS header in the same change that enables Cloudflare HSTS
  at 31536000; do not leave two independently managed HSTS headers;
- enable includeSubDomains only after every subdomain permanently supports
  HTTPS; do not enable preload yet;
- do not enable Authenticated Origin Pulls for the Tunnel origin.

The exact target rule below requires at least a Business plan because it uses
the request method, a 60-second counting window, and a 600-second mitigation
timeout:

~~~text
http.request.method eq "POST" and http.request.uri.path in {
  "/api/v1/auth/register"
  "/api/v1/auth/login"
  "/api/v1/auth/login/2fa"
  "/api/v1/auth/passkey/login/begin"
  "/api/v1/auth/passkey/login/finish"
  "/api/v1/auth/send-verify-code"
  "/api/v1/auth/oauth/pending/send-verify-code"
  "/api/v1/auth/forgot-password"
  "/api/v1/auth/reset-password"
}
~~~

Count by visitor IP, allow 20 requests per 60 seconds, and block for 10
minutes. The application's Redis-backed fail-closed endpoint limits remain the
second layer.

On the Free plan, use the one available rule with only the URI-path portion of
the expression above, count by visitor IP, allow 5 requests per 10 seconds,
and block for 10 seconds. Free supports only path matching, IP counting, a
10-second counting period, and a 10-second mitigation timeout; it cannot match
the POST method or reproduce 20 requests per minute with a 10-minute block.
Keep the application Redis limits as the authoritative second layer, and do
not report the Free fallback as equivalent to the target rule.

Final acceptance requires TLS 1.0/1.1 rejection, TLS 1.2/1.3 success, HSTS and
DNSSEC verification, failed direct IPv4 and IPv6 origin access, forged
CF-Connecting-IP rejection, and successful login, API, SSE, WebSocket, upload,
and admin workflows.
