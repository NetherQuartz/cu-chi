# Củ Chi

Self-hosted family VPN management with Synology SSO, individual device profiles and VPS deployment.

**Status: core VPN management, monitoring and desktop Web Push are deployed; managed LAN gateway is under acceptance testing.** Synology login, real Ubuntu 26.04 VPS deployment, external tunnel traffic, revocation, NAS backups/logs, server probes and desktop Chrome notifications have passed acceptance. Native mobile notification/profile-import checks and real VM/Keenetic gateway acceptance remain pending.

## Current functionality

- Synology OIDC login with group-based administrator and user roles.
- User-scoped device inventory; administrators can see all devices.
- Administrator-only VPS inventory with encrypted passwords/private keys, reusable SSH keys and pinned host fingerprints (automatic first-use pinning or a manually verified pin).
- Semi-automatic Ubuntu 24.04 / 26.04 LTS amd64 deployment, durable retries and main-server selection.
- Administrator-managed LAN gateway VM with password/saved-key SSH deployment, stable Keenetic WireGuard profiles and fail-closed WG → AWG routing; real household acceptance pending.
- Per-device encrypted keys, `.conf`/QR export, reissue and confirmed peer revocation.
- Server CPU/RAM/disk/network dashboards and traffic statistics by user, device and device/server pair.
- Country flags/city metadata, separate profile currency and periodic tunnel/DNS/HTTPS health checks.
- Debounced outages/recoveries, maintenance mode, per-user inbox and targeted Web Push with preferences.
- Current-browser test push and honest transport status; iOS Home Screen installation support.
- Daily NAS-local backups with 14 successful bundles retained; system-following dark theme and OS icons.
- Session expiration, CSRF protection and a mutation audit log.
- Russian responsive interface with no separate frontend build step.
- PostgreSQL migrations and Docker Compose configuration.
- CI tests and publishes Linux amd64/arm64 images to GHCR.

See [VPS deployment and profile operations](docs/vpn-operations.md) and [local Keenetic gateway](docs/local-gateway.md) for prerequisites, migration behavior and limits.

## Install on a NAS

Prerequisites: Docker Compose / Container Manager, reachable PostgreSQL container, HTTPS reverse proxy and Synology SSO Server configured for OIDC. The portal and SSO must be reachable without a working VPN. Confirm compatibility with the actual Xpenology installation.

1. Download `compose.yaml`, `.env.example`, and `deploy/backup/backup.sh` to a dedicated directory on the NAS. Save the script there as `backup.sh` (readable mode 644). Create the private `backups` directory before starting the project; DSM does not automatically create missing bind-mount directories.
2. Copy `.env.example` to `.env` and restrict access to it (`chmod 600 .env`).
3. Generate the database password with `openssl rand -hex 32` and the encryption key with `openssl rand -base64 32`. Keep the encryption key stable and back it up separately from database dumps.
4. Register an OIDC application in Synology with the exact redirect URI `https://<portal-domain>/auth/callback`. Copy the issuer from its discovery document and fill the client ID/secret into `.env`.
5. Check the actual group claim values. The defaults are `administrators` and `vpn-users`; no group means no access. Register the users in Synology, not in this portal.
6. For a proxy running directly on the NAS host, forward to `127.0.0.1:8080`. Compose binds locally by default. A proxy inside a container has its own loopback address: use the container-proxy installation below. If the proxy is on another host, deliberately change `CUCHI_BIND_ADDRESS` and firewall access.
7. Select a successful published `sha-<full-commit>` tag in `CUCHI_IMAGE_TAG` and run:

```sh
docker compose pull
docker compose up -d
docker compose ps
```

The published image is anonymously accessible from GHCR; no GitHub login or token is needed to pull it. This was checked using an empty Docker configuration.

The app validates OIDC discovery at startup and fails closed if configuration or certificates are invalid. It runs as a non-root user with a read-only filesystem. It does not need privileged mode, the Docker socket or VPN capabilities on the NAS.

### Nginx Proxy Manager in Docker

Use `compose.npm.yaml` as a **standalone replacement** for `compose.yaml`, not an override. In Synology Container Manager, save it as `compose.yaml` in the new project directory alongside the private `.env` and `backup.sh` files.

Set `CUCHI_PROXY_NETWORK` to the existing Docker network containing NPM (find it in Container Manager → Network). The application joins that network with the alias `cuchi`; PostgreSQL stays on the separate project network. This variant publishes no NAS host ports. Use a unique alias if deploying multiple instances.

In NPM create an HTTPS proxy host for the portal, forwarding to `http://cuchi:8080`, with a valid certificate and Force SSL enabled. Leave asset caching disabled. Do not add NPM Basic Auth in front of the OIDC callback. Configure Synology SSO with its own public HTTPS hostname and the exact portal callback `/auth/callback`; read the issuer from the discovery document, not from a guessed URL.

Keep the project directory and `.env` accessible only to NAS administrators. The encryption key must survive updates and have a separate private backup. When using the original filename from this repository, add `-f compose.npm.yaml` to every Compose command.

### Container Manager logs

Production Compose files inherit the Docker engine's default logging driver. This lets Synology use its own logger for the Container Manager UI. The application writes to stderr and the backup completion message to stdout; no extra log volume is needed. An explicit `json-file` override can hide otherwise valid container output from DSM's UI ([first-hand report on Synology Community](https://community.synology.com/enu/forum/72/post/187350)). Existing projects must remove their service-level `logging` blocks and recreate the containers to use the engine default. Image updates alone do not change the existing Compose configuration. Log retention then follows the host logger's configuration; `max-size`/`max-file` options for `json-file` are not assumed to work with Synology's driver. On ordinary Linux installations, configure bounded log retention at the Docker daemon or in an explicit host-specific override.

### Updating and rollback

CI publishes `main` and `sha-<full-commit>` after successful main-branch checks. A pushed `v*` release tag publishes an image with that same tag. Use SHA or release tags for a deliberate update:

```sh
# Back up the currently installed version before editing .env.
docker compose exec -T backup /bin/sh /usr/local/bin/cuchi-backup --once
# Now change CUCHI_IMAGE_TAG in .env.
docker compose pull app
docker compose up -d --wait app backup
```

Recreate the backup service along with the app so future bundles record the new image tag. Keep the previous tag and a database backup. To enable the backup service on an existing installation, also copy `backup.sh`, update Compose and recreate the full project. Migration 002 adds VPN state. After profiles are issued, do not roll back to an inventory-only version: it cannot revoke their peers. See the [recovery notes](docs/vpn-operations.md#persistence-and-recovery). Never use `docker compose down -v` during an ordinary update.

## Browser notifications

In **Уведомления**, connect each browser/device separately. On iPhone/iPad add the portal to the Home Screen and launch it there before requesting permission. Use **Отправить тест себе** to test the current browser only; other devices on your account are not included. The portal reports transport acceptance, not proof that the notification appeared or sounded. Critical severity requests high Web Push urgency but respects device Focus/sound settings.

The encrypted VAPID identity and subscriptions are stored in PostgreSQL and covered by the existing database/master-key backups. No separate push credentials are required. NAS outbound HTTPS must reach the supported Google FCM, Apple or Mozilla push endpoints. The service worker does not cache profiles or authenticated responses. See [notification behavior and platform limits](docs/health-notifications.md).

## Local development

Requirements: Go 1.26.11 and Docker Compose. Older Go installations with automatic toolchain selection download the required version. No Node.js installation is needed.

```sh
docker compose -f compose.dev.yaml up -d --wait
# Create an ignored local configuration once:
(umask 077; printf 'export CUCHI_DEV_AUTH=true\nexport CUCHI_LISTEN=127.0.0.1:8080\nexport CUCHI_PUBLIC_URL=http://127.0.0.1:8080\nexport CUCHI_DATABASE_URL="postgres://cuchi:local-development-only@127.0.0.1:55432/cuchi?sslmode=disable"\nexport CUCHI_MASTER_KEY="%s"\n' "$(openssl rand -base64 32)" > .env.local)
. ./.env.local
go run ./cmd/cuchi
```

Open `http://127.0.0.1:8080`. The development login is explicitly local-only; the app rejects a public listening address or mismatched public URL in this mode. It creates a development administrator, not a production account. Do not expose this mode through a reverse proxy or SSH forwarding to untrusted users.

```sh
export TEST_DATABASE_URL='postgres://cuchi:local-development-only@127.0.0.1:55432/cuchi?sslmode=disable'
gofmt -w cmd internal
go vet ./...
go test -race ./...
```

Without `TEST_DATABASE_URL`, PostgreSQL integration tests report a skip. CI always supplies it. Test fixtures generate ephemeral keys and use reserved addresses.

## Documentation

- [Architecture and security decisions](docs/architecture.md)
- [Roadmap and acceptance criteria](docs/roadmap.md)
- [Health checks and notifications](docs/health-notifications.md)
- [Agent/session handoff](docs/handoff.md)
- [Contributor and agent instructions](AGENTS.md)

No license has been selected yet; do not assume this repository grants reuse rights beyond GitHub's default terms.

## Daily backups on the NAS

Both production Compose files include a backup service using the official PostgreSQL image. It takes an initial snapshot after the app/database are healthy, then every 24 hours while running; failures retry after one hour. The default bind directory is `./backups` beside Compose, separate from the database's Docker volume. `CUCHI_BACKUP_DIR` can select another private NAS directory. The service initializes ownership of this directory for root and sets mode 700, including folders created through DSM File Station. Only the CHOWN capability is added; all others remain dropped.

Each completed bundle contains `database.dump` (custom-format pg_dump), `master-key` and the application `image-tag`. These are sensitive, unencrypted files protected by mode 600 inside mode-700 directories. They allow recovery of encrypted SSH/VPN credentials; keep the backup directory admin-only. Other deployment configuration remains in the private project `.env` and Compose; preserve those files too. This local copy does not protect against losing the NAS/storage itself.

The script checks the archive with `pg_restore --list`, publishes it by atomic directory rename and retains the newest 14 completed bundles. Failed snapshots never remove older successful ones. The backup container becomes unhealthy on failure or if the last success is older than 26 hours; this does not yet send a push notification. For an immediate backup, run `docker compose exec backup sh /usr/local/bin/cuchi-backup --once`. Concurrent manual/scheduled runs are serialized by a file lock. The schedule restarts when the container is recreated.

Restore into an empty PostgreSQL 17 database using `pg_restore --exit-on-error --dbname=<database> database.dump`, restore the saved master key into the private `.env`, and use the corresponding application SHA tag. Test restoration before switching the portal to that database; existing VPS peers must then be reconciled. CI tests actual restoration, private file permissions, failure handling and retention. Do not copy the live PostgreSQL data directory as a logical backup.
