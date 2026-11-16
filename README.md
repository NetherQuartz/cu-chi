# Củ Chi

Self-hosted family VPN management with Synology SSO, individual device profiles and VPS deployment.

**Status: initial foundation.** Authentication, device inventory, encrypted server credentials and an audit log are implemented. VPN deployment, profile generation and Web Push are not yet implemented. This version does not connect to registered VPSs or create working VPN profiles.

## Current functionality

- Synology OIDC login with group-based administrator and user roles.
- User-scoped device inventory; administrators can see all devices.
- Administrator-only VPS inventory with encrypted SSH keys and pinned host fingerprints.
- Session expiration, CSRF protection and a mutation audit log.
- Russian responsive interface with no separate frontend build step.
- PostgreSQL migrations and Docker Compose configuration.
- CI tests and publishes Linux amd64/arm64 images to GHCR.

## Install on a NAS

Prerequisites: Docker Compose / Container Manager, reachable PostgreSQL container, HTTPS reverse proxy and Synology SSO Server configured for OIDC. The portal and SSO must be reachable without a working VPN. Confirm compatibility with the actual Xpenology installation.

1. Download `compose.yaml` and `.env.example` to a dedicated directory on the NAS.
2. Copy `.env.example` to `.env` and restrict access to it (`chmod 600 .env`).
3. Generate the database password with `openssl rand -hex 32` and the encryption key with `openssl rand -base64 32`. Keep the encryption key stable and back it up separately from database dumps.
4. Register an OIDC application in Synology with the exact redirect URI `https://<portal-domain>/auth/callback`. Copy the issuer from its discovery document and fill the client ID/secret into `.env`.
5. Check the actual group claim values. The defaults are `administrators` and `vpn-users`; no group means no access. Register the users in Synology, not in this portal.
6. Configure an HTTPS reverse proxy to `127.0.0.1:8080`. Compose binds locally by default. If the proxy is on another host, deliberately change `CUCHI_BIND_ADDRESS` and firewall access.
7. Select a successful published `sha-<full-commit>` tag in `CUCHI_IMAGE_TAG` and run:

```sh
docker compose pull
docker compose up -d
docker compose ps
```

If the GHCR package is private, authenticate on the NAS with a GitHub token that has `read:packages`. Never put that token into Compose or Git. Package visibility is independent of repository visibility and must be checked after the first publication.

The app validates OIDC discovery at startup and fails closed if configuration or certificates are invalid. It runs as a non-root user with a read-only filesystem. It does not need privileged mode, the Docker socket or VPN capabilities on the NAS.

### Updating and rollback

CI publishes `main` and `sha-<full-commit>` after successful main-branch checks. A pushed `v*` release tag publishes an image with that same tag. Use SHA or release tags for a deliberate update:

```sh
# Change CUCHI_IMAGE_TAG in .env first.
docker compose pull app
docker compose up -d app
```

Keep the previous tag and a database backup. An image rollback is safe only when its code supports the currently applied database schema; future releases must document migration compatibility. Never use `docker compose down -v` during an ordinary update.

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
- [Agent/session handoff](docs/handoff.md)
- [Contributor and agent instructions](AGENTS.md)

No license has been selected yet; do not assume this repository grants reuse rights beyond GitHub's default terms.
