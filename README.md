<p align="center">
  <img src="caretakarr-assets/logo/caretakarr-hero.png" alt="Caretakarr: real domains, real HTTPS, for your arr stack" width="820">
</p>

# Caretakarr

One container that puts your arr stack (Sonarr, Radarr, Plex and friends) behind real domains on Cloudflare with valid HTTPS certificates. A small web UI manages domains and routes. [Caddy](https://caddyserver.com) runs embedded in the same Go process, and its configuration is generated from SQLite and applied atomically.

- Certificates via ACME DNS-01 on Cloudflare: no inbound ports are needed for issuance, and LAN-only upstreams work.
- Per domain, one wildcard certificate (default) or one certificate per host.
- Creates and removes the matching Cloudflare A records (DNS-only, never proxied), including the record for the UI host itself, so servers with several IPs resolve to the one you choose.
- Per-route enable/disable, CIDR allowlist and basic auth.
- Upstream health, certificate expiry, DNS state and a live Caddy/ACME log in the UI.
- Optional Docker discovery: list running containers and pre-fill a route from one.
- Built-in help for people new to DNS and TLS: a hint under every field, a live preview of what *Add site* will create, plain-language explanations of errors and status words, a getting-started checklist, and a Help page.
- Single static binary on a distroless image; all state in one `/data` volume.

**LAN and tailnet only.** Every address Caretakarr publishes must be private IPv4 (10/8, 172.16/12, 192.168/16) or CGNAT (100.64/10, Tailscale). Public IPs, IPv6 and Cloudflare-proxied (orange cloud) records are not supported.

## Quickstart

1. Create a Cloudflare API token (see below).
2. Copy the example environment file and fill it in:

   ```sh
   cp .env.example .env
   ```

   `CLOUDFLARE_API_TOKEN`, `CARETAKARR_HOST`, `CARETAKARR_HOST_IP` and `ACME_EMAIL` are required. Set `ADMIN_PASSWORD` too.
3. Make the data directory writable by the container user (UID 65532):

   ```sh
   mkdir -p data && sudo chown 65532:65532 data
   ```
4. Start it. Caretakarr creates the `CARETAKARR_HOST` A record itself (pointing at `CARETAKARR_HOST_IP`), so there is no manual DNS step:

   ```sh
   docker compose up -d
   ```
5. Open `https://<CARETAKARR_HOST>`. The dashboard's *UI host* card shows the record's state. Click **Add site** and enter the hostname (`oc.example.com`) and its backend: host or IP plus its own **Backend port** field (`192.168.25.165` and `9200`). Presets fill the port, and the Docker socket (if mounted) lists containers to pick from. Caretakarr finds the Cloudflare zone, adds the domain if it is new, and creates the route and A record. That is the Caddyfile equivalent of `oc.example.com { reverse_proxy http://192.168.25.165:9200 }`.

If the zone for `CARETAKARR_HOST` can't be found, set `CARETAKARR_HOST_ZONE`. If a different A record already exists for that name, the card shows a conflict and a *Take over* button; nothing is overwritten until you click it.

To try it without burning Let's Encrypt rate limits, use the staging CA:

```sh
docker compose -f docker-compose.yml -f docker-compose.dev.yml up --build
```

## Cloudflare token

Create a custom API token at *My Profile → API Tokens* with:

- `Zone : Zone : Read`
- `Zone : DNS : Edit`

scoped to the zones you want to manage. The same token is used for certificate issuance (DNS-01) and for the A records. It is read from the environment only; it is never stored in SQLite, logs or the generated Caddy config.

## Configuration

| Variable | Required | Meaning |
|---|---|---|
| `CLOUDFLARE_API_TOKEN` | yes | Token above. |
| `CARETAKARR_HOST` | yes | Hostname the UI is served on. It gets its own certificate. |
| `CARETAKARR_HOST_IP` | yes | Address `CARETAKARR_HOST` resolves to; Caretakarr creates and syncs that A record. Private IPv4 only: 10/8, 172.16/12, 192.168/16, or 100.64/10 (Tailscale). The same rule applies to every domain and route IP. |
| `CARETAKARR_HOST_ZONE` | no | Cloudflare zone name for that record. Unset walks up the host's labels. |
| `ACME_EMAIL` | yes | Let's Encrypt contact; a bare address. |
| `ADMIN_PASSWORD` | no | Enables login. **Unset means the UI is open** (warning in the log and a red banner). |
| `CARETAKARR_SECRET_KEY` | no | At least 32 characters (`openssl rand -hex 32`). Encrypts the authenticator-app seed in SQLite; without it that option is off. Back it up with `/data`: if it is lost, run `2fa-reset`. |
| `REQUIRE_2FA` | no | `true`: a password login must be followed by an authenticator code, and a password-only session is limited to the Security page until one is set up. Needs `ADMIN_PASSWORD` and `CARETAKARR_SECRET_KEY`. |
| `TURNSTILE_SITE_KEY`, `TURNSTILE_SECRET` | no | Both or neither. Adds a Cloudflare Turnstile check to the password form on `CARETAKARR_HOST`. The `UI_LISTEN` recovery address is exempt. Passkey and code steps have no widget. |
| `ACME_ISSUER` | no | `internal` serves self-signed certificates from Caddy's local CA (offline setups and tests). Default is Let's Encrypt. |
| `ACME_STAGING` | no | `true` uses the Let's Encrypt staging CA. |
| `ACME_PROPAGATION_DELAY`, `ACME_PROPAGATION_TIMEOUT` | no | DNS-01 tuning, e.g. `2m` / `10m`. Unset uses Caddy's defaults. |
| `ACME_RESOLVERS` | no | Default `1.1.1.1,8.8.8.8`. |
| `UI_LISTEN` | no | Default `127.0.0.1:8080`. |
| `DATA_DIR` | no | Default `/data`. |
| `HTTP_PORT`, `HTTPS_PORT` | no | Caddy listen ports, default 80 and 443. |
| `DOCKER_SOCKET` | no | Default `/var/run/docker.sock`. Discovery is active only when the socket exists. |

## Warnings

**`UI_LISTEN` is a recovery hatch, not a default.** The UI listens on loopback and Caddy proxies `CARETAKARR_HOST` to it. If Caddy is broken you can set `UI_LISTEN=0.0.0.0:8080` and publish the port to fix things, but then the UI is reachable without TLS. Without `ADMIN_PASSWORD` that is a full authentication bypass. Remove it again afterwards.

**Two-factor and passkeys.** With `ADMIN_PASSWORD` set, the Security page adds an authenticator app (with 10 one-time backup codes) and passkeys. A passkey sign-in skips the code. Passkeys work on `https://CARETAKARR_HOST` and on `localhost`, and are tied to that hostname: changing `CARETAKARR_HOST` makes existing ones unusable. Lost everything? `docker compose run --rm caretakarr caretakarr 2fa-reset` removes the authenticator, backup codes and passkeys (it needs only the data volume); restart afterwards to end open sessions.

**Magic-link site access.** A site's form (add and edit) can require a magic link before it is served: either an allowlist of addresses and `@domains`, or open to any address. The link is sent by email, single-use, 15 minutes. Email delivery is configured once under **Settings** (SMTP host, port, user, MAIL FROM; an optional password is stored encrypted — for that, `CARETAKARR_SECRET_KEY` must be in .env). Sessions are shared across the gated sites that allow that address and slide 30 days (allowlist) or 7 days (open) while in use; revoking one identity under Security cuts it on every gated site at once. This is an access barrier for staging sites, not full authentication: a site that needs real auth must do its own.

**The Docker socket is root on your host.** Mounting `/var/run/docker.sock` enables container discovery, and gives anything that can reach Caretakarr's UI the same power over Docker. Keep it off unless you need it, set `ADMIN_PASSWORD`, and prefer a read-only socket proxy such as [`tecnativa/docker-socket-proxy`](https://github.com/Tecnativa/docker-socket-proxy) (point `DOCKER_SOCKET` at its socket).

**Volume permissions.** The image runs as UID/GID 65532 (distroless `nonroot`). A host directory mounted at `/data` must be writable by that user, otherwise Caretakarr cannot create its database or certificate storage. Named volumes need nothing.

**Backups.** Copy `/data` (SQLite database and Caddy's certificate storage).

## How it works

Every change writes SQLite, renders the full Caddy JSON, and loads it with `caddy.Load`. If Caddy rejects it, the database change is rolled back, the previous configuration stays live, and the UI shows Caddy's exact error. If the stored configuration fails to load at startup, Caretakarr serves a bootstrap configuration (the UI only) and shows a banner instead of crash-looping.

Cloudflare records Caretakarr creates are marked `managed by caretakarr` and are the only ones it will change or delete. An identical existing record is adopted and never touched; a different one is reported as a conflict, with an explicit *Take over* action. The same rules apply to the UI host's record, which is synced at startup and from the dashboard, and never deleted automatically.

A route's A record points at its domain's default IP unless the route sets its own *DNS record IP*. A DNS failure never reverts a route: the dashboard shows the error with a *Retry* button.

A route can't use the hostname of `CARETAKARR_HOST`, since the UI route always wins in Caddy.

## Development

```sh
make test    # go test -race ./...
make lint    # gofmt + golangci-lint
make e2e     # Playwright against the built binary (bun + chromium)
make check   # all of the above
make build   # static binary in bin/
make docker  # image
```

Design notes are in `docs/DESIGN.md`, requirements in `docs/REQUIREMENTS.md`.

## License

MIT, see `LICENSE`. Third-party notices (Caddy, caddy-dns/cloudflare: Apache-2.0) are in `NOTICE` and shipped in the image under `/licenses`.

The hero artwork shows Cloudflare, Docker and arr-app logos for illustration only; it implies no affiliation with or endorsement by those projects.
