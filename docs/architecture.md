# Architecture

## Deployment boundaries

The NAS hosts the control plane: web application, PostgreSQL, future background jobs and Web Push sender. Synology SSO Server remains the identity provider. External VPSs host the VPN data plane. A local Linux gateway may later bridge ordinary WireGuard from a Keenetic router to AmneziaWG on a VPS.

Existing external VPN connections must not depend on portal availability. The local gateway must continue using its last applied configuration if the portal stops. Control-plane routes must remain independent of the VPN they repair.

## Technology choice

Go keeps the API, embedded frontend and future deployment worker in one distributable binary. The project uses standard-library HTTP routing and established OIDC/OAuth, PostgreSQL and SSH libraries. This choice favors a compact NAS installation and networking automation; throughput is not the deciding factor at family scale. Django would provide more built-in CRUD/admin functionality. The owner asked for the rationale; no alternative stack was requested.

The current UI is HTML/CSS/JavaScript embedded in the binary. A future frontend framework can replace it without changing the protocol or ownership model. PostgreSQL holds durable state and will also support a transactional job queue; Redis is not required for the initial scope.

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

Actual Synology discovery and the administrator browser login have been validated on the owner's NAS. The groups claim was accepted and mapped to the administrator role. An ordinary-user login remains to be tested. Deployment credentials stay in private NAS configuration and are never stored in Git.

## Secrets and audits

The 32-byte base64 master key is loaded from environment. SSH private keys are encrypted with AES-256-GCM using a new nonce and server-ID-associated data. Key material is never returned by list APIs, recorded in audit events or emitted to logs. This protects backups at rest, not a compromised running NAS.

Current SSH input accepts a dedicated private key without a passphrase; the database encrypts it at rest. The fingerprint is supplied from a trusted provider console. Actual SSH host-key verification must be implemented before any connection or deployment command is added. Do not use `InsecureIgnoreHostKey`.

Audits record actor, action, resource ID and timestamp in the same transaction as the mutation. They do not record request payloads. All responses are `Cache-Control: no-store`; a future PWA service worker must never cache authenticated APIs, profile downloads or secrets.

Master-key rotation, encrypted backups and restore exercises remain future work. Preserve the original master key across container updates.

## Current data model

`users`, `sessions`, `login_attempts`, `devices`, `servers`, `audit_events`, `schema_migrations`.

Devices are inventory entries, not active VPN peers. Servers are registered SSH targets, not deployed VPN services. Current deletion removes only inventory because there are no actual peers yet. This endpoint must become an orchestrated revocation operation before profiles are introduced.

SQL migrations are embedded, ordered, checksummed and applied transactionally under an advisory lock. Applied files cannot be edited. Add new migrations for future schema changes.

## Next data model and worker

Add protocol services, encrypted per-device credentials, profile revisions, desired/applied state, jobs and job attempts. Use a transactional outbox/queue for deployment and revocation. A retry must not create extra peers; reserve identities once and reconcile intended state. Lock per device/server where needed. Do not mark a profile ready until it is applied and verified on the target.

A device can have distinct bindings for old/new servers during migration. A re-download returns the same revision. Explicit reissue creates fresh credentials. Compromise revocation and routine migration have different overlap policies. Devices behind one routed gateway do not each need the gateway's private key.

The initial deployer will use SSH, a pinned supported OS/runtime and versioned idempotent scripts. Use official AmneziaWG 3.1+ userspace tools; pin tested source revisions and validate obfuscation settings. Deployments must test tunnel traffic and DNS, not just process state or handshakes. No raw scripts accepted from user input.

## Notifications and extensions

Web Push uses VAPID, service workers and explicit browser consent. Browser subscriptions belong to users and are independent of VPN devices. iOS/iPadOS needs an installed Home Screen web app. Push acceptance is not proof of delivery or profile import. Reminders target users with outstanding device revisions, with deduplication, retries, unsubscribe and expired-subscription cleanup.

The gateway, WARP and VLESS are separate adapters. WARP egress does not cure an inaccessible VPS ingress. VLESS requires a compatible client and cannot be imported into an AmneziaWG-only app.

## Delivery

GitHub Actions runs tests with PostgreSQL before building and publishing a static, non-root container for amd64/arm64. Final images are published to GHCR from CI only. `main` is convenient for development; a SHA/release tag is preferred on the NAS. Package visibility must be verified independently. Update/rollback instructions must state database compatibility.
