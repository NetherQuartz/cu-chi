# Củ Chi — agent guide

Read `docs/handoff.md` first, then `docs/architecture.md` and `docs/roadmap.md`.
Keep these files current when behavior, constraints or verification results change.

## Product constraints

- Product name: **Củ Chi**. Repository/image slug: `cu-chi`. No slogan.
- A self-hosted family VPN portal, hosted on DSM 7.2 / Xpenology.
- Users sign in through Synology SSO (OIDC). Admin authority comes from a verified Synology group.
- The owner provisions existing VPSs semi-automatically. Users manually reissue and import their own profiles.
- Existing AmneziaWG apps on iOS, Android, Windows and macOS remain in use. Do not build a native VPN client.
- AmneziaWG **3.1 or newer** is mandatory. Do not silently downgrade exported parameters.
- One independent tunnel device gets its own identity/keys. A routing gateway can represent multiple LAN devices.
- Browser Web Push reminders are planned; iOS requires a Home Screen web app and explicit permission.
- A local ordinary WireGuard → AmneziaWG gateway is planned for a Keenetic router.
- WARP egress and VLESS are future adapters, not assumptions embedded into device records.

## Implementation

- Go standard-library HTTP server and embedded HTML/CSS/JavaScript; PostgreSQL with pgx.
- Official OIDC and OAuth libraries; validate issuer, audience, signature, expiry, state, nonce and PKCE.
- Keep roles and ownership enforcement on the backend. Treat missing groups as denied access.
- Check ownership in SQL. Mutations require an exact Origin and a session CSRF token.
- SSH private keys and passwords use AES-256-GCM, bound to server:<id> or ssh-key:<id> associated data. Never return private keys/passwords in APIs; saved-key APIs expose only public material.
- Applied SQL migrations are immutable; append new numbered migrations.
- No arbitrary shell commands from request input. Future SSH deployment must verify pinned host keys.
- Never show fabricated deployment success, VPN profiles, connectivity, metrics or push delivery.
- Do not commit secrets, real endpoints, account details, private keys, configs, database dumps or private conversation transcripts.
- Use reserved domains/IPs and dynamically generated ephemeral test keys in fixtures.

## Workflow

- Early-development commits and pushes to `main` are authorized by the owner.
- Build and publish deliverable images through GitHub Actions; do not publish images from a workstation.
- CI must test before publishing `ghcr.io/netherquartz/cu-chi` for amd64 and arm64.
- The NAS uses prebuilt images. Preserve SHA-tagged versions for rollback; do not rely only on a moving tag.
- Run `gofmt`, `go vet ./...` and `go test -race ./...` with `TEST_DATABASE_URL` configured.
- Integration tests use isolated temporary PostgreSQL schemas and must run in CI, not silently skip there.
- For UI changes, check desktop and mobile layouts and real interactions when a browser is available.
- Update the handoff with what passed, what remains unverified and the next concrete step.
