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

## VPN implementation (2026-11-17)

Added durable deployment/sync/check jobs, strict pinned SSH, Ubuntu 24.04 amd64 preflight, official-source AWG 3.1 runtime, temporary-peer tunnel/DNS/TLS verification, default server selection and per-device encrypted profile revisions. Users can issue/reissue/download/scan/revoke; deletion waits for confirmed revocation. Offline old-server revocations remain visible. See `docs/vpn-operations.md` for exact behavior and limits.

Application CI now waits for the AWG runtime test/publish job and embeds its immutable digest. The NAS application remains amd64/arm64; the first supported VPS runtime is amd64. This implementation and the SSO recovery fix are deployed to the NAS at `sha-f0dc9cd18230327f65bd1ddbefbb3006a278c462`. See the release verification below.

Local verification: vet and PostgreSQL race/integration tests cover ownership, encryption, retry identity, stale attempt completion, concurrent revocation, default-server migration and worker restart recovery. SSH tests exercise a real local handshake, host-key mismatch, context cancellation and shell quoting. Three real Docker TUN runs passed encrypted 64 KiB transfers, NAT through a third container, repeated reconciliation and revoked-peer denial. UI registration/deployment form and 390px mobile layout passed; no real VPS deployment was claimed.

Docker Desktop's external TCP path supplied invalid TCP checksums (observed before and after AWG), causing public HTTPS to fail despite internal tunnel/NAT and public DNS working. Do not silently remove the egress test or treat this as a successful external test. CI enables the public DNS/HTTPS check on Ubuntu Linux; every real VPS deployment also requires it. The production runtime contains no diagnostic packet-capture tooling or Desktop-specific workaround.

A dedicated test VPS is being created by the owner; none has been supplied yet. Real VPS provisioning, external inbound UDP and official iOS/Android/Windows/macOS profile import remain acceptance checks. The owner confirmed ordinary-user login after adding the account to the configured user group. The later access-denied screen was resolved by that membership change. No Web Push, WARP, local ordinary-WG gateway, VLESS, automatic retirement or grace-period policy exists yet.

## Verification

- `go vet ./...` passed.
- `go test -race -count=1 ./...` passed with PostgreSQL integration tests enabled.
- Production Compose parsed successfully with non-secret placeholder configuration.
- Browser checks passed: local login, device creation, duplicate rejection, persistence after reload/relogin, audit display and logout. Layouts checked at 390px and 1440px widths.
- Local preview used only generated test data. `.env.local` is ignored.
- NAS execution and real Synology administrator OIDC login passed on 2026-11-17 (see NAS integration below). Ordinary-user login was subsequently confirmed by the owner; real VPS connections remain untested.
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

Read AGENTS.md, architecture and operations. NAS currently runs the tested `f0dc9cd` application. The next acceptance step is registering the owner's fresh VPS with a trusted host fingerprint and dedicated SSH key, deploying it and validating a real client profile. Do not mark those steps complete based on fake-transport API tests or local containers. Then implement `docs/health-notifications.md`; this milestone must distinguish profile currency from runtime updates and server health, with no automatic probes from user networks. Infrastructure credentials belong only in private deployment configuration.

## NAS integration verified (2026-11-17)

- Browser access to DSM/NPM is working. Synology SSO Server is installed, and OIDC has been enabled behind a dedicated HTTPS proxy. Discovery advertises RS256, S256 PKCE and the groups scope. A portal client with an exact callback has been registered. The published image started successfully in Container Manager with PostgreSQL. HTTPS readiness returns 200 and anonymous `/api/me` returns 401. The owner completed the real browser OIDC flow and the portal correctly displayed the administrator role.
- Added standalone `compose.npm.yaml` for a containerized proxy: application joins the existing proxy network under alias `cuchi`, database stays on the project network, no host ports are published. Do not merge it with the host-proxy Compose file.
- Infrastructure addresses and all credentials stay outside this public repository. Never commit deployment screenshots or filled environment files.
- NAS installation currently pins `sha-15b8991755a724a5feb658cf243a88ff6a7bbc26`; the NPM Compose change does not modify application code. Project configuration is separate from the database volume. The configuration directory ACL was inspected and permits only the owner, the built-in administrator and the administrators group.
- NPM Compose commit `57bb6ea3be6dd2cf8a80359004af727088b65553` passed formatting, vet, race/integration tests, vulnerability scanning, build and multi-architecture publishing: https://github.com/NetherQuartz/cu-chi/actions/runs/43977943295.
- The browser extension needs file-URL access for uploads. After the owner enabled it, Compose and private environment files uploaded through File Station and Container Manager used the existing project directory.
- Created the empty `vpn-users` DSM group without granting NAS folder/application permissions. The owner subsequently added an ordinary user and confirmed successful login.

## SSO callback recovery

A user reported an intermittent token-exchange error followed by a successful login after navigating back. The original provider error was discarded, so the cause cannot be established retrospectively. Callback errors now render an HTML recovery page with retry/open-portal links and a correlation reference; server logs retain only stage, whitelisted OAuth error category and upstream HTTP status. They never log codes, tokens, callback URLs, provider bodies or error descriptions. Client authentication is selected from discovery instead of probing/retrying an authorization code after an arbitrary error. Existing state, PKCE, nonce, signature and group checks remain required. Automated tests cover rejection, replay, no session creation on failure and redaction. After update, real administrator login through Synology succeeded; an invalid callback rendered the new recovery page over public HTTPS. The originally reported intermittent failure has not been reproduced.

First VPN CI run `43985412457` passed application tests but failed on Linux fixture ownership before starting the tunnel. Test fixtures now permit read access to the capability-restricted container; production configurations remain root-owned mode 0600. Follow-up CI passed, including the real public DNS/HTTPS egress test on Ubuntu Linux.

## Release verification and next steps

- Application commit `f0dc9cd18230327f65bd1ddbefbb3006a278c462` passed CI: https://github.com/NetherQuartz/cu-chi/actions/runs/43986269342.
- Published application index: `sha256:a6ab401215854c343258c40c6975435aa927628499deaed0a6650e015ce73fe3`; amd64 and arm64 verified by registry manifest.
- Published AWG runtime index: `sha256:6402636adb221e5cb70a9eac4d1df948176d96278834d742d36eac76a50319cb`; embedded in that application build. Both packages passed anonymous manifest access.
- Linux CI passed real AWG traffic, NAT, public DNS/TLS, identical reapplication and revoked-peer denial. The Desktop-specific external checksum issue was not present on the Ubuntu runner.
- Local browser tests with a temporary fake-transport fixture passed issuance, ready-state download link, a loaded 512×512 QR and revocation. This fixture was explicitly labelled as simulated and is stopped/removed. No synthetic deployment success was written to production.
- NAS update used the existing project's `.env`, changing only the image tag. A PostgreSQL custom-format backup was created in the private database volume before migration, verified with `pg_restore --list`, and chmod 600. Project recreation preserved the database volume and master key. The new app started, HTTPS readiness returned 200, the real administrator SSO flow succeeded and invalid callbacks displayed HTML recovery UI.
- Still awaiting the dedicated Ubuntu 24.04 amd64 test VPS and trusted SSH access. No real VPS has been deployed and no official client import has been tested yet.
- New owner requirements are recorded in `docs/health-notifications.md`: visible server health/profile currency, critical app-level severity, targeted outage/recovery pushes, detailed administrator incidents and noise filtering. Automatic user-network probes are explicitly outside scope. These notification/monitoring features remain the next milestone, not part of the deployed release.

## SSH access expansion (2026-11-17)

The owner requested password login and a key that can be configured once and reused for new VPSs. Added admin-only saved-key creation (generated Ed25519/import), public-key copying, server key selection and server-specific password/private-key input. Private material is never exported. Saved keys have independent AEAD binding; old server credentials retain their namespace. Deletion is blocked by a database FK while referenced. Password authentication is standard SSH password only, with pinned host verification before authentication. Non-root deployment still needs passwordless sudo; no workstation SSH-agent integration, automatic key installation or credential rotation is implied.

Local vet and PostgreSQL race/integration tests passed, including upgrade from migrations 001–002 with an existing encrypted credential, idempotent migration, shared-key worker resolution, password encryption/whitespace, API authorization/CSRF, deletion constraints and real localhost password SSH. The host-pin test confirms that a rejected host receives no password. Browser checks covered generated public-key display, shared-key registration, password registration at 390px and an existing legacy server record. CI publication and NAS update are pending. The owner has now supplied a private local VPS credential file; its contents and location are deliberately omitted here. The owner explicitly selected automatic trust on first use. A first host observation was saved privately before password authentication, then reused for a successful read-only VPS check: Ubuntu 26.04 amd64, root, TUN, inactive UFW, systemd running and docker.io available. No infrastructure addresses or secrets are recorded here.

The owner authorized TOFU as the default registration flow, with manual verified fingerprints optional. Registration observes the host key without authentication, stores it, and all later jobs pin it. Migration 004 records the source. The first observation is not independent verification; a later mismatch never rotates the pin. Added Ubuntu 26.04 LTS to preflight alongside 24.04 after inspecting the supplied host. Full deployment/traffic acceptance is still pending. Added the owner's reference dashboard requirements to health-notifications.md (server network rates/history, CPU/cores, RAM and disk, stale-data state).

## Local NAS backups requested

Added a daily PostgreSQL backup sidecar to both production Compose files (official postgres:17-alpine, no new custom image). Installation now requires the repository's `deploy/backup/backup.sh` alongside Compose as `backup.sh`. The bind-mounted private backups folder is separate from the database volume. Bundles contain a validated custom-format dump, original master key and image tag, mode 600; retain 14 successful bundles, retry failures hourly, report failure/staleness through container health. Deployment configuration is still preserved separately. Restore, permission, retention and failure-path tests passed locally; CI also runs them. No cloud/offsite replication is configured. NAS rollout is pending.

## Follow-up fixes being validated

The owner noticed repeated logouts. A freshly authenticated production session had only about 161 seconds remaining; callback code incorrectly bounded RP session lifetime to the short-lived ID token. Changed this to an independent 30-minute portal session after all OIDC verification; expired assertions, bad nonce/audience and missing groups still fail. This follows OpenID Connect Core's distinction between assertion expiry and RP session duration. Added a short-lived signed-token regression test.

System-following dark mode was requested, explicitly AMOLED: the page canvas uses #000000 while subtly lighter dark cards preserve readability. The owner clarified that making every surface black was not requested. The browser preview can force only the dark CSS branch locally for visual inspection without changing the owner's OS preference.

DSM requires the backup bind directory to exist before container startup. The File Station-created directory also belongs to a NAS user; the backup service now has only CHOWN in addition to dropped capabilities, initializes the backup directory's root ownership and mode 700. The integration test reproduces the non-root-owned directory and checks restore/retention. Full NAS backup verification is still pending.

OS-specific icons are now vendored SVGs (Tabler, plus Simple Icons Linux/Tux), with license notices in the repository and container image. Linux is a first-class user device platform through migration 005. External icon CDNs/fonts are not used.
