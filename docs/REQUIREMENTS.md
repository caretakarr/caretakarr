# Caretakarr — Requirements

Status: draft from `/sc:brainstorm`. License: MIT. Next step: `/sc:design`.

## Goal

One Docker container, one Go process, that lets self-hosters put their arr stack behind a real domain they manage on Cloudflare, with valid HTTPS certificates and no inbound ports required for issuance. A simple web UI manages domains and routes. Caddy config is fully generated from SQLite.

## Decisions made

| Topic | Decision |
|---|---|
| Caddy topology | Embedded in the Go binary (`caddy/v2` + `caddy-dns/cloudflare` as Go imports, `caddy.Load`). No xcaddy, no supervisor, no separate admin-API client. Needs a spike to confirm. |
| UI auth | Optional `ADMIN_PASSWORD` in `.env`. If unset, UI is open and a loud startup warning is logged. |
| DNS scope | Caretakarr creates/updates/deletes Cloudflare A records per route, using the official `cloudflare-go` SDK. |
| Upstream entry | Manual entry with arr presets by default. Docker socket discovery is opt-in, only active if the user mounts the socket. |
| Hostname model | Subdomain per service under managed domains. One wildcard cert per domain. |
| Frontend | Go templates + htmx + Pico or Tailwind, served from `embed.FS`. No Node build step. |
| Route controls (v1) | Enable/disable toggle, LAN-only IP allowlist, per-route basic auth. |

## Configuration (.env)

- `CLOUDFLARE_API_TOKEN` (required): scoped token with `Zone:Read` and `DNS:Edit`. Same name as the homelab Caddyfile's `{$CLOUDFLARE_API_TOKEN}`, so existing `.env` files drop in.
- `ACME_PROPAGATION_DELAY` (optional, unset = Caddy default), `ACME_PROPAGATION_TIMEOUT` (optional, unset = Caddy default): DNS-01 tuning, only emitted into the config when set. A working homelab config uses `2m` / `10m`.
- `ACME_RESOLVERS` (default `1.1.1.1,8.8.8.8`).
- `CARETAKARR_HOST` (required): hostname for the Caretakarr UI itself, e.g. `caretakarr.example.com`.
- `CARETAKARR_HOST_IP` (required): the address `CARETAKARR_HOST` resolves to; Caretakarr manages that A record. IPv4 in 10/8, 172.16/12, 192.168/16 or 100.64/10 only. The same rule applies to every domain and route IP: Caretakarr never publishes public addresses. See `docs/DESIGN.md` §16.
- `CARETAKARR_HOST_ZONE` (optional): Cloudflare zone name for that record; unset means walk up the host's labels.
- `ADMIN_PASSWORD` (optional): enables UI login.
- `ACME_EMAIL` (required): Let's Encrypt account contact (expiry and problem notices). Must parse as a valid address (`net/mail`). Caddy itself treats it as optional, Caretakarr requires it and fails fast at startup naming the variable.
- `ACME_STAGING` (optional): use the Let's Encrypt staging CA.

## Functional requirements

1. **Bootstrap.** On first boot, with an empty database, generate a Caddy config from `.env` alone that serves the UI at `CARETAKARR_HOST` with a valid certificate.
2. **Domains.** Add and remove domains. Validate that the domain is a zone reachable by the token. Reject `.local`, `.internal`, `.home.arpa`, IP literals and other names Caddy would send to its internal CA.
3. **Certs.** Per-domain `cert_mode`: `wildcard` (default, one `*.domain` cert) or `per_host` (one cert per route hostname, like a plain Caddyfile). Both via DNS-01 with Cloudflare. Apply propagation delay and timeout only if set, and resolvers from `.env`. Switching mode re-renders and Caddy issues what is missing.
4. **Routes.** A route is subdomain + upstream host/IP + port + scheme (http/https, with skip-verify option for self-signed upstreams) under a managed domain.
5. **Presets.** Built-in arr presets with default ports: Sonarr, Radarr, Lidarr, Readarr, Prowlarr, Bazarr, Overseerr, Jellyseerr, qBittorrent, SABnzbd, Tautulli, Plex, Jellyfin, Emby. The preset fills the subdomain and port and can be overridden.
6. **DNS records.** On route create, update and delete, manage the matching A record in Cloudflare. Each route gets a DNS-only (grey cloud) record by default. Record IP is user-supplied per domain (default target IP). Show the sync state of each record.
7. **Route controls.** Enable/disable toggle, optional CIDR allowlist (403 outside), optional basic auth (bcrypt hash stored in SQLite).
8. **Docker discovery (opt-in).** If a Docker socket is mounted, list containers and exposed ports, and pre-fill a route from one. Hidden when no socket is present.
9. **Apply loop.** Every change writes SQLite, regenerates the full Caddy JSON, and applies it atomically. If Caddy rejects the config, the previous config stays live, the UI shows the error, and the DB change is reverted.
10. **Status.** Per-route health (upstream reachable), per-domain cert state (issued / pending / error, expiry), per-record DNS state.
10a. **Logs page.** Caddy log lines (including ACME/issuance errors) captured in an in-memory ring buffer and shown in the UI, filterable by level. Not persisted.
11. **Auth.** If `ADMIN_PASSWORD` is set: login page, session cookie (HttpOnly, SameSite), rate-limited attempts. Optional second factors (authenticator app, passkeys), optional Turnstile on the password step, `REQUIRE_2FA`, and a `2fa-reset` command: see `docs/DESIGN.md` §17. Separately, a per-route optional **site access gate**: the site asks for a magic link (single-use token emailed via SMTP) before anything is proxied, with per-domain or open enrollment, shared sessions and operator revocation; email delivery is configured on the Settings page in SQLite with a sealed password: see `docs/DESIGN.md` §18.
12. **Persistence.** All state in one SQLite file plus Caddy's cert storage, both under one `/data` volume. Backup is copying `/data`.

## Non-functional requirements

- Single static binary, multi-stage Dockerfile, image on a minimal base. Multi-arch (amd64, arm64).
- One volume (`/data`), ports 80 and 443 for Caddy, no separate admin port. No Caddy admin API exposed on the network.
- Idle memory target under 100 MB. Cold start to serving under 5 seconds with existing certs.
- Secrets never written to SQLite or logs. The token is read from the environment only.
- Structured logs (`slog`) with a configurable level.
- Tests: unit tests for config rendering, integration test applying configs to an in-process Caddy with a fake upstream, mocked Cloudflare API for DNS sync.
- MIT license. Ship `LICENSE` and a `NOTICE` file crediting Caddy and the Cloudflare module (both Apache-2.0).

## User stories and acceptance

- *As a self-hoster,* I set `CF_API_TOKEN` and `CARETAKARR_HOST`, run `docker compose up`, and open `https://CARETAKARR_HOST` with a valid cert. **Accept:** no manual Caddy or DNS steps beyond pointing that host at the server.
- *I add `example.com`.* **Accept:** wildcard cert issues, status shows the expiry date.
- *I add Sonarr at `192.168.1.20:8989` as `sonarr`.* **Accept:** `https://sonarr.example.com` works with a valid cert, websockets included, and the A record exists in Cloudflare.
- *I restrict a route to my LAN.* **Accept:** a request from outside the CIDR gets 403.
- *I submit a bad upstream or Cloudflare error.* **Accept:** the live config is unchanged and the UI shows the exact error.
- *I restart the container.* **Accept:** all routes come back from SQLite with no re-issuance.

## Risks

- Embedded Caddy lifecycle (signal handling, reload from inside the process) is unverified. Spike first.
- The A-record IP for LAN-only setups may differ from the public IP. Per-domain default IP is required, and dynamic IP/DDNS is out of scope.
- Docker discovery gives the container host-level power. Document it, recommend a read-only socket proxy, keep it off by default.
- Open UI with no `ADMIN_PASSWORD` plus a public hostname is a footgun. The startup warning is the only guard in v1.
- Let's Encrypt rate limits. Default staging for first run is worth considering.

## Open questions

1. Default A-record target: auto-detect the public IP, or require the user to enter an IP per domain?
2. Should Cloudflare-proxied (orange cloud) records be supported at all, given they break private IPs?
3. Does deleting a route also delete its DNS record, or only after confirmation?
4. First-run default: staging CA or production?
5. Import/export of config (JSON) in v1 or later?
6. Should `CARETAKARR_HOST`'s domain have to be added as a managed domain, or is it auto-registered?
