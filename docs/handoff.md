# Session handoff

Last updated: 2026-11-17. This file intentionally contains no private infrastructure details or credentials.

## Owner decisions

- Name: Củ Chi; no slogan.
- Existing public GitHub repository; early-development commits/pushes to main are permitted.
- Store agent instructions, architecture decisions and session handoff in this repository, without confidential data.
- Build and publish deliverable Docker images through GitHub CI so the NAS can update by changing image tags.
- Host control plane on DSM 7.2/Xpenology; Synology SSO and administrator group are authoritative.
- Users keep their existing AmneziaWG apps and manually reissue/import device profiles.
- Browser push reminders are wanted, including the documented iOS PWA installation step.
- Keenetic ordinary-WG local gateway, WARP and VLESS are later stages.

## Implemented in the first working slice

Go HTTP server, embedded Russian responsive UI, PostgreSQL migrations, OIDC code flow with PKCE/state/nonce and group roles, opaque sessions, CSRF/ownership checks, device inventory, encrypted SSH credential inventory and audit log. A loopback-only development login allows UI testing without real SSO credentials.

GitHub Actions is configured to test with PostgreSQL and publish amd64/arm64 GHCR images. Compose consumes published images; it does not build on the NAS.

## Not implemented / do not imply otherwise

No SSH connection, AmneziaWG deployment, key/profile generation, main-server migration, notifications, gateway, WARP or VLESS yet. Registered devices have no VPN profiles, and registered VPSs are not deployed. Real Synology and NAS compatibility are unverified.

## Verification

- `go vet ./...` passed.
- `go test -race -count=1 ./...` passed with PostgreSQL integration tests enabled.
- Production Compose parsed successfully with non-secret placeholder configuration.
- Browser checks passed: local login, device creation, duplicate rejection, persistence after reload/relogin, audit display and logout. Layouts checked at 390px and 1440px widths.
- Local preview used only generated test data. `.env.local` is ignored.
- Real Synology OIDC, NAS execution and real VPS connections are not yet tested.
- The original workstation Go 1.26.5 failed vulnerability scanning. The project and Dockerfile now require the patched official Go 1.26.11; the system installation is untouched.
- All tests and vet passed again on Go 1.26.11; `govulncheck v1.9.0` reported no vulnerabilities. The official builder image was verified and pinned by digest.
- First CI run succeeded: https://github.com/NetherQuartz/cu-chi/actions/runs/43972362121 (application commit `44963354a2e967b17bc3702af2ce7941513cadd5`).
- CI published `ghcr.io/netherquartz/cu-chi:main` and `sha-44963354a2e967b17bc3702af2ce7941513cadd5` for Linux amd64 and arm64. Index digest: `sha256:20f61626d48727a87746d830ade4c2587b8b63e2df11f9eb57ac65b6e7550651`.
- Anonymous manifest access passed with an empty Docker configuration. No package token is needed on the NAS.
- Both published architecture variants started successfully with PostgreSQL and a test-only HTTPS OIDC discovery endpoint. UI returned 200, readiness passed, anonymous `/api/me` returned 401, and `/cuchi healthcheck` succeeded. Containers ran as UID/GID 65532 with read-only filesystems and all capabilities dropped. AMD64 was exercised under Docker Desktop emulation.
- These container checks do not verify real Synology login or real NAS networking.
- CI checkout/setup-go actions were updated to their current v7 releases and pinned by commit after GitHub warned about the older Node 20 runtime; the runner is explicitly Ubuntu 24.04 to avoid implicit OS migrations.
- Temporary preview/smoke processes and containers are stopped after verification. The ignored local configuration and development database volume remain for local development.

## Resume

Read AGENTS.md, architecture and roadmap. Next functional slice is durable jobs + strict SSH preflight + one supported AmneziaWG 3.1+ deployment. Keep current security/ownership guarantees. Do not extend inventory deletion to deployed peers until revocation is implemented.

For actual NAS installation, the owner will need to configure the portal HTTPS URL, Synology discovery issuer/client settings and verified group names in a private `.env`. Do not ask them to paste secrets into an issue or commit.
