# Architecture

## Deployment boundaries

The NAS hosts the control plane: web application, PostgreSQL, background jobs and Web Push sender. Synology SSO Server remains the identity provider. External VPSs host the VPN data plane. A local Linux gateway may later bridge ordinary WireGuard from a Keenetic router to AmneziaWG on a VPS.

Existing external VPN connections must not depend on portal availability. The local gateway must continue using its last applied configuration if the portal stops. Control-plane routes must remain independent of the VPN they repair.

## Technology choice

Go keeps the API, embedded frontend and deployment worker in one distributable binary. The project uses standard-library HTTP routing and established OIDC/OAuth, PostgreSQL and SSH libraries. This choice favors a compact NAS installation and networking automation; throughput is not the deciding factor at family scale. Django would provide more built-in CRUD/admin functionality. The owner asked for the rationale; no alternative stack was requested.

The current UI is HTML/CSS/JavaScript embedded in the binary. A future frontend framework can replace it without changing the protocol or ownership model. PostgreSQL holds durable state and a transactional job queue; Redis is not required for the initial scope.

## Identity and authorization

- OIDC Authorization Code flow with S256 PKCE, nonce and browser-bound state.
- Discovery and token signatures verified by `go-oidc`; issuer and audience must match configuration.
- `groups` must be present in a verified ID token. Exact configured admin-group membership grants administrator; user-group membership grants user; everything else is denied.
- The identity key is `(issuer, subject)`, never an unverified email or browser-supplied role. Synology subjects may be username-based; account renames/recreation require deliberate reconciliation, not automatic ownership transfer.
- Opaque random session cookies are HttpOnly/Secure/SameSite=Lax in production. Only token hashes are stored in PostgreSQL.
- Session lifetime is bounded to 30 minutes or the ID token expiry, whichever is earlier. Groups refresh at login, not continuously. A new denied login blocks that user's existing sessions; external group removal alone is not instant revocation.
- Backend roles and SQL ownership checks protect resources; hiding navigation is only presentation.
- Unsafe authenticated requests require the exact configured Origin and a session CSRF token.
- Development authentication requires explicit opt-in, an IP loopback listener and an identical loopback HTTP origin. It is never enabled by production Compose.

Browser login errors use an HTML recovery page and a reference for safe log correlation. Provider error descriptions, token bodies and callback URLs are never logged. Client authentication uses the advertised discovery method without automatic code-redeeming retries.

Actual Synology discovery and the administrator browser login have been validated on the owner's NAS. The groups claim was accepted and mapped to the administrator role. The owner also confirmed ordinary-user login after adding that account to the configured user group. Deployment credentials stay in private NAS configuration and are never stored in Git.

## Secrets and audits

The 32-byte base64 master key is loaded from environment. SSH private keys and passwords are encrypted with AES-256-GCM using a new nonce and server-ID-associated data. Reusable SSH keys have their own `ssh-key:<id>` associated-data namespace and are referenced by servers, rather than copied. Key material is never returned by list APIs, recorded in audit events or emitted to logs. This protects backups at rest, not a compromised running NAS.

SSH input accepts a server-specific private key without a passphrase, a password, or a saved-key reference. Administrators can generate Ed25519 keys or import existing private keys once; only their public portion is returned. Saved keys are immutable and cannot be deleted while referenced by any server. Public keys must be installed on each VPS through its provider or trusted console; this does not automatically enroll VPSs. Password SSH uses the standard SSH password method, without keyboard-interactive/OTP or password-based sudo. Root or unrestricted passwordless sudo remains required. The administrator chooses TOFU or a manually verified fingerprint. TOFU performs an unauthenticated handshake during server registration and persists the first host fingerprint before allowing authenticated jobs. It cannot detect substitution on that first observation; a console-verified fingerprint remains available. A saved pin is never automatically replaced. SSH connections verify this pinned fingerprint before authentication and commands. Do not use `InsecureIgnoreHostKey`.

Audits record actor, action, resource ID and timestamp in the same transaction as the mutation. They do not record request payloads. All responses are `Cache-Control: no-store`; a future PWA service worker must never cache authenticated APIs, profile downloads or secrets.

Master-key rotation, encrypted backups and restore exercises remain future work. Preserve the original master key across container updates.

## VPN state and worker

The foundation tables are joined by `profiles`, `jobs` and the singleton `vpn_preferences`. Server settings and per-profile private keys/PSKs use the same encrypted store, with separate associated-data namespaces. Profiles have immutable revisions, unique public keys and unique monotonically allocated IPv4 addresses per server. Only one revision per device is desired at a time.

Mutations and job enqueueing share short PostgreSQL transactions. A single worker owns a session advisory lock; SSH runs outside transactions. A restart recovers unfinished work. Jobs merge queued changes per server, retry up to three attempts and carry an attempt token to reject late results. The worker sends a consistent desired-state snapshot. Remote `flock` plus a persisted generation fence prevent an old operation from overwriting a newer snapshot.

A profile becomes downloadable only after applying and verifying the peer. Concurrent revocation cannot be undone by completion of an older snapshot. A device cannot be deleted until all remaining peers are confirmed removed. Re-download preserves keys; reissue allocates new credentials, queues old-peer removal and new-peer installation. Selecting the default server alone changes no existing profile. There is no grace period in this slice; old-server revocation can remain pending while a new-server profile becomes ready.

The deployment adapter supports a dedicated Ubuntu 24.04/26.04 LTS amd64 VPS with root or passwordless sudo and TUN. It installs Docker if absent and runs an official-source, version/digest-pinned AmneziaWG 3.1 userspace image. IPv4 NAT runs inside a dedicated container namespace; Docker publishes the selected UDP port. The NAS never needs a Docker socket or networking capabilities. A temporary peer verifies tunnel traffic, DNS and HTTPS egress before readiness. This does not verify inbound UDP through the provider firewall or connectivity from a censored client network.

SQL migrations remain embedded, ordered, checksummed and applied transactionally under an advisory lock. Applied migrations are immutable. See [operations and recovery](vpn-operations.md) for runtime source pins and rollback limits.

## Notifications and extensions

Web Push uses VAPID, service workers and explicit browser consent. Browser subscriptions belong to users and are independent of VPN devices. iOS/iPadOS needs an installed Home Screen web app. Push acceptance is not proof of delivery or profile import. Reminders target users with outstanding device revisions, with deduplication, retries, unsubscribe and expired-subscription cleanup.

The gateway, WARP and VLESS are separate adapters. WARP egress does not cure an inaccessible VPS ingress. VLESS requires a compatible client and cannot be imported into an AmneziaWG-only app.

## Delivery

GitHub Actions runs tests with PostgreSQL before building and publishing a static, non-root container for amd64/arm64. Final images are published to GHCR from CI only. `main` is convenient for development; a SHA/release tag is preferred on the NAS. Package visibility must be verified independently. Update/rollback instructions must state database compatibility.
