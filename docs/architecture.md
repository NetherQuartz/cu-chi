# Architecture

## Deployment boundaries

The NAS hosts the control plane: web application, PostgreSQL, background jobs and Web Push sender. Synology SSO Server remains the identity provider. External VPSs host the VPN data plane. A local Linux gateway may later bridge ordinary WireGuard from a Keenetic router to AmneziaWG on a VPS.

Existing external VPN connections must not depend on portal availability. The local gateway must continue using its last applied configuration if the portal stops. Control-plane routes must remain independent of the VPN they repair.

## Technology choice

Go keeps the API, embedded frontend and deployment worker in one distributable binary. The project uses standard-library HTTP routing and established OIDC/OAuth, PostgreSQL and SSH libraries. This choice favors a compact NAS installation and networking automation; throughput is not the deciding factor at family scale. Django would provide more built-in CRUD/admin functionality. The owner asked for the rationale; no alternative stack was requested.

The current UI is HTML/CSS/JavaScript embedded in the binary. Active sections use an allowlisted `?view=` query parameter with browser history, preserving the section on reload; unknown or unauthorized sections fall back to Devices. Profile submission shows immediate request progress, followed by server-pending progress until polling observes readiness or a reported failure. A future frontend framework can replace it without changing the protocol or ownership model. PostgreSQL holds durable state and a transactional job queue; Redis is not required for the initial scope.

## Identity and authorization

- OIDC Authorization Code flow with S256 PKCE, nonce and browser-bound state.
- Discovery and token signatures verified by `go-oidc`; issuer and audience must match configuration.
- `groups` must be present in a verified ID token. Exact configured admin-group membership grants administrator; user-group membership grants user; everything else is denied.
- The identity key is `(issuer, subject)`, never an unverified email or browser-supplied role. Synology subjects may be username-based; account renames/recreation require deliberate reconciliation, not automatic ownership transfer.
- Opaque random session cookies are HttpOnly/Secure/SameSite=Lax in production. Only token hashes are stored in PostgreSQL.
- Portal sessions use a 30-minute idle timeout. Trusted foreground interaction (pointer/keyboard/input/wheel/touch), foreground return and initial page entry send an authenticated Origin/CSRF-protected activity POST, coalesced to at most once per 30 seconds per tab. A trailing request preserves recent burst activity; hidden tabs discard pending work. Reads/background polling and idle timers do not renew sessions. Renewal atomically checks current expiry and role again, extends only that session in PostgreSQL, and refreshes its HttpOnly cookie. Expired/deleted/denied sessions cannot be revived. Continuous interaction can extend a session without an absolute lifetime cap. ID-token expiry is checked when accepting the OIDC assertion; it does not shorten the independent portal session. Synology may issue assertions lasting only a few minutes. Groups refresh at login, not continuously. A new denied login blocks that user's existing sessions; external group removal alone is not instant revocation.
- Backend roles and SQL ownership checks protect resources; hiding navigation is only presentation.
- Unsafe authenticated requests require the exact configured Origin and a session CSRF token.
- Development authentication requires explicit opt-in, an IP loopback listener and an identical loopback HTTP origin. It is never enabled by production Compose.

Browser login errors use an HTML recovery page and a reference for safe log correlation. Provider error descriptions, token bodies and callback URLs are never logged. Client authentication uses the advertised discovery method without automatic code-redeeming retries.

Actual Synology discovery and the administrator browser login have been validated on the owner's NAS. The groups claim was accepted and mapped to the administrator role. The owner also confirmed ordinary-user login after adding that account to the configured user group. Deployment credentials stay in private NAS configuration and are never stored in Git.

## Secrets and audits

The 32-byte base64 master key is loaded from environment. SSH private keys and passwords are encrypted with AES-256-GCM using a new nonce and server-ID-associated data. Reusable SSH keys have their own `ssh-key:<id>` associated-data namespace and are referenced by servers, rather than copied. Key material is never returned by list APIs, recorded in audit events or emitted to logs. This protects backups at rest, not a compromised running NAS.

SSH input accepts a server-specific private key without a passphrase, a password, or a saved-key reference. Administrators can generate Ed25519 keys or import existing private keys once; only their public portion is returned. Saved keys are immutable and cannot be deleted while referenced by any server. Public keys must be installed on each VPS through its provider or trusted console; this does not automatically enroll VPSs. Password SSH uses the standard SSH password method, without keyboard-interactive/OTP or password-based sudo. Root or unrestricted passwordless sudo remains required. The administrator chooses TOFU or a manually verified fingerprint. TOFU performs an unauthenticated handshake during server registration and persists the first host fingerprint before allowing authenticated jobs. It cannot detect substitution on that first observation; a console-verified fingerprint remains available. A saved pin is never automatically replaced. SSH connections verify this pinned fingerprint before authentication and commands. Do not use `InsecureIgnoreHostKey`.

Audits record actor, action, resource ID and timestamp in the same transaction as the mutation. They do not record request payloads. All responses are `Cache-Control: no-store`; a future PWA service worker must never cache authenticated APIs, profile downloads or secrets.

Master-key rotation and encryption of entire backup bundles remain future work. NAS-local database/master-key backups and isolated restore exercises are implemented. Preserve the original master key across container updates.

## VPN state and worker

The foundation tables are joined by `profiles`, `jobs` and the singleton `vpn_preferences`. Server settings and per-profile private keys/PSKs use the same encrypted store, with separate associated-data namespaces. Profiles have immutable revisions, unique public keys and unique monotonically allocated IPv4 addresses per server. Only one revision per device is desired at a time.

Mutations and job enqueueing share short PostgreSQL transactions. A single worker owns a session advisory lock; SSH runs outside transactions. A restart recovers unfinished work. Jobs merge queued changes per server, retry up to three attempts and carry an attempt token to reject late results. The worker sends a consistent desired-state snapshot. Remote `flock` plus a persisted generation fence prevent an old operation from overwriting a newer snapshot.

A profile becomes downloadable only after applying and verifying the peer. Concurrent revocation cannot be undone by completion of an older snapshot. A device cannot be deleted until all remaining peers are confirmed removed. Profile downloads use application/octet-stream with an explicit .conf attachment name. Filenames are ASCII and at most 15 characters before the extension for AmneziaWG Android import: abbreviated owner/server/device labels plus revision and a short profile fingerprint. Very large revision numbers use a compact profile fingerprint fallback. Full labels, decimal revision and UTC issuance time are comment-only metadata; control characters are flattened to prevent configuration injection. QR content remains the original configuration. Re-download preserves keys; reissue allocates new credentials, queues old-peer removal and new-peer installation. Selecting the default server alone changes no existing profile. There is no grace period in this slice; old-server revocation can remain pending while a new-server profile becomes ready.

The deployment adapter supports a dedicated Ubuntu 24.04/26.04 LTS amd64 VPS with root or passwordless sudo and TUN. It installs Docker if absent and runs an official-source, version/digest-pinned AmneziaWG 3.1 userspace image. IPv4 NAT runs inside a dedicated container namespace; Docker publishes the selected UDP port. The NAS never needs a Docker socket or networking capabilities. A temporary peer verifies tunnel traffic, DNS and HTTPS egress before readiness. This does not verify inbound UDP through the provider firewall or connectivity from a censored client network.

SQL migrations remain embedded, ordered, checksummed and applied transactionally under an advisory lock. Applied migrations are immutable. See [operations and recovery](vpn-operations.md) for runtime source pins and rollback limits.

## Notifications and extensions

Web Push uses VAPID, service workers and explicit browser consent. Browser subscriptions belong to users and are independent of VPN devices. iOS/iPadOS needs an installed Home Screen web app. Push acceptance is not proof of delivery or profile import. Reminders target users with outstanding device revisions, with deduplication, retries, unsubscribe and expired-subscription cleanup.

The gateway, WARP and VLESS are separate adapters. WARP egress does not cure an inaccessible VPS ingress. VLESS requires a compatible client and cannot be imported into an AmneziaWG-only app.

## Delivery

GitHub Actions runs tests with PostgreSQL before building and publishing a static, non-root container for amd64/arm64. Final images are published to GHCR from CI only. `main` is convenient for development; a SHA/release tag is preferred on the NAS. Package visibility must be verified independently. Update/rollback instructions must state database compatibility.


## Monitoring and measured traffic

The worker samples deployed servers through the same pinned SSH transport, leadership and remote flock used for deployment, without modifying peers or restarting the runtime. Raw host samples and per-device/server traffic deltas retain 30 days; lifetime observed totals preserve ownership and device names separately from disposable profile generations. Initial counters and resets start a baseline. Missing/old samples are not zero utilization, and handshake recency is not proof of internet connectivity. Public service responses contain only friendly location/status metadata. Resource history and infrastructure diagnostics are administrator-only. Collection samples CPU interval deltas, MemAvailable, root filesystem use and the default IPv4 interface. See kernel proc documentation: https://kernel.org/doc/html/v6.15/filesystems/proc.html.

Country and city are optional administrator-entered metadata. Flags are vendored ISO assets; no external icon or geolocation request occurs on page load. User traffic queries enforce owner scope in SQL, including historical/deleted-device records. Period summaries can be approximate at missed sampling intervals; they are not provider billing counters.

## Periodic health and Web Push

Migrations 007–009 add health state, maintenance, per-user inbox, encrypted VAPID/subscriptions, preferences and the durable push queue. The periodic synthetic probe serializes with deployments but changes only its unique temporary peer. Its result is generation-checked again in the database. Counters and deployment generations are not reset by the probe. Failed checks are debounced into durable incident transitions; management failures do not imply data-plane failure.

Push delivery has a separate worker so a slow SSH job does not delay notifications. Claims have leases and retries are bounded. Delivery checks the current database role, preferences and outstanding audience; OIDC group changes become visible at the next login, matching existing authorization semantics. Subscription capabilities and VAPID private key are encrypted with separate associated-data namespaces. All required identity state is included in existing database/master-key backups. A restored older backup may require browser reconnection, and can replay an external delivery whose acceptance was recorded after that backup; notification tags reduce duplicates but cannot provide exactly-once delivery.

The service worker uses a dedicated 96×96 white-on-transparent PNG badge for Android status-bar masking, separate from the full-color notification/app icon. Its editable source is web/badge.svg; regenerate with `sips -s format png internal/httpapp/web/badge.svg --out internal/httpapp/web/badge-96.png` on macOS (or an SVG renderer preserving alpha). The service worker does not cache pages, APIs or configuration files. Ordinary-user push content is generic and contains no VPN keys, device names, addresses or diagnostics. Administrator operation failures additionally include bounded initiator usernames and a friendly server name; merged jobs retain all initiators starting with migration 013. The self-test endpoint targets only the requesting user's supplied, already-owned browser subscription. Read-state suppresses pending delivery; being shown in the inbox is not a claim that the push was delivered. See health-notifications.md for concrete defaults and platform limits.

Manual reminder requests use an administrator-scoped UUID and a transactionally stored result. There is no per-day send cap. A distinct explicit action can send immediately, while retrying the same UUID cannot expand the original audience. Default-server changes have independent campaign IDs and expire pending older migration messages. Request deduplication metadata retains 90 days, matching the inbox. Production Compose inherits the host logging driver for DSM compatibility; log retention follows the host configuration.

## Managed local gateway

Migration 011 separates gateway VM inventory/credentials and its durable operation state from public VPN servers; a VM can never become the main VPS by accident. Each gateway owns one ordinary device record and immutable encrypted LAN WG identity. Passwords use gateway:<id> associated data; saved SSH keys retain their existing namespace. The VPS worker shares its leadership with gateway deployment, prefers pending VPS jobs and recovers interrupted gateway attempts. Generation fences, remote flock and attempt tokens reject stale operations/results. Gateway deployment distinguishes interface readiness from usable uplink: bounded DNS/HTTPS retries allow cold handshake establishment without container restarts, and only a successful probe marks the deployment ready. Final errors expose allowlisted stage codes only. Profile deployment is explicit after issuance; the current and last-applied profile IDs are kept separately. See local-gateway.md for routing, policy and acceptance boundaries.


Migration cleanup failures caused by SSH connection failure/timeout on a non-default server are informational only when no desired peer is pending there and every revoking device has a desired replacement elsewhere. The failed job and unconfirmed peer removal persist, but their informational notices do not generate pushes. Other failures still alert. Device cards separate pending current-profile failure from older revocation, and never treat an unreachable old server as proof that its keys were removed. Migration 013 adds job actor membership and notice context without changing profile/key/runtime state.

## Per-server WARP egress

Migration 014 adds desired/applied egress modes (both default direct) and an encrypted per-server WARP registration. An administrator selects the mode with CSRF protection; enabling requires explicit Cloudflare terms acknowledgement. Queue generations fence concurrent changes while preserving all AWG profile keys, endpoint, addresses and revision. The worker registers via pinned unofficial wgcf on the VPS only when first requested; raw registration never enters logs/APIs and is encrypted under `warp:<server-id>`. Root-only VPS registration caching survives interrupted NAS saves.

Inside the VPS container only, `iif awg0` routes through kernel WG `warp0` when enabled. An unreachable table default plus a dedicated forwarding chain block direct fallback, including interface loss. Locally originated management/AWG/WARP outer packets use main routing. Switching clears only AWG-source conntrack flows. Startup forwarding is blocked until the complete egress policy exists. IPv6 remains disabled. A failed application restores the prior mode best-effort; rollback failure is distinct and visible. Successful production apply/probe requires DNS, HTTPS and Cloudflare `warp=on|plus`, not just a handshake. See [operations/limitations](warp-egress.md).

Real WARP acceptance on a dedicated Ubuntu 26.04 VPS is recorded in [warp-egress.md](warp-egress.md): registration, unchanged external AWG profile, Cloudflare exit, UDP DNS/TLS, restart, fail-closed failure/recovery and failed-enable rollback passed. This did not change the family server or the NAS; real portal toggle and QUIC/native-client UI acceptance remain distinct.


Xray uses an independent revision queue, REALITY identity per VPS, and server-specific VLESS credentials per enrolled device. See [Xray](xray.md).

## Server editing and deletion

Migration 017 gives each VPS an edit revision and an optional deletion timestamp. The SSH address also supplies the Xray subscription host. The AWG public endpoint can be edited independently. A saved edit preserves the SSH host fingerprint and the VPN identity. Concurrent edits use the management revision. An empty credential field retains an existing secret only for the same login method. Per-server advisory locks serialize edits and deletion against remote work. Gateway deployments consuming that server's profile are also checked.

Deletion is a portal operation and works for offline hosts. One transaction removes saved access and Xray runtime configuration, releases the SSH address and optional default, cancels pending work, and archives outstanding AWG profiles. Archived profiles may still be desired so migration reminders include their owners until reissue or explicit withdrawal. History retains the server ID, including its traffic and inbox links, previously applied Xray device associations and gateway uplink. Read models share a generated display name with the deletion marker. Remote processes and previously installed keys can remain on the VPS.
