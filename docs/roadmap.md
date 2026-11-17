# Roadmap

## Foundation (implemented)

- OIDC and group-based roles.
- Isolated device inventory, administrator server inventory, encrypted SSH keys/passwords, reusable generated/imported SSH keys.
- Audit log, migrations, responsive UI, Compose and GitHub CI/GHCR images.
- Automated authentication, ownership and encryption tests.

## VPS deployment and real profiles (implemented, acceptance pending)

1. Add durable jobs and desired/applied state with retries and idempotency.
2. Implement strict SSH host-key pinning and a connection/preflight check.
3. Add a single tested OS target and pinned official AmneziaWG 3.1+ runtime.
4. Deploy tunnel, forwarding/NAT/firewall and restart behavior without replacing unrelated host rules.
5. Verify traffic using a temporary unique peer; detect DNS/MTU failures.
6. Add per-device credentials, server bindings and immutable profile revisions.
7. Export `.conf` and suitable QR codes tested in current AmneziaWG clients on iOS/Android/Windows/macOS.
8. Make reissue/withdrawal durable, auditable operations. Offline-server revocations stay pending visibly.
9. Main-server selection and voluntary user migration are implemented. A configurable overlap deadline remains future work; current reissue immediately queues old-key removal.

Implemented code includes steps 1–8; real VPS deployment, external AWG traffic and revocation passed. Official native-client acceptance remains pending. Expiry, configurable grace periods and retirement controls remain future work.

Acceptance: admin registers a VPS and deploys it; a normal user generates two distinct working device profiles; another user cannot access either; reissue/retry creates no duplicates; withdrawal really removes the server peer.

## Health, profile status and browser reminders

Detailed requirements: [health and notifications](health-notifications.md). Monitor server-side operation only; no automatic user-network probes. Display profile currency separately from runtime updates and peer reconciliation. Critical severity uses ordinary Web Push with high transport urgency, without promising native iOS Critical Alerts.

- Manifest, service worker, VAPID, browser subscription lifecycle.
- Explicit consent and an iOS Home Screen installation guide.
- Admin preview/test/send, user selection and stale-profile audiences.
- Inbox, deduplication, bounded retries and optional scheduled reminders.
- Notification opens the authenticated device list; no secrets in payloads or URLs.

## Local gateway (implemented, acceptance in progress)

- Separate Ubuntu VM confirmed available in VMM; administrator SSH deployment from the portal.
- Ordinary LAN WG from Keenetic to the gateway, AWG from gateway to a VPS; stable LAN keys across uplink reissues.
- Direct WAN routing for tunnel transport and management traffic; separate client-only routing tables.
- Owner selected fail-closed. Runtime packet tests cover DNS/HTTPS/MTU, failed uplink and recovery; real router DNS/IPv6/no-fallback policy needs household acceptance.
- Details and limits: [local gateway](local-gateway.md).

## Egress and protocol adapters

- Validate WARP forwarding end-to-end, including UDP/QUIC and failure behavior.
- Start with a server-level egress policy.
- Add Xray/VLESS through a separate adapter and profile exporter.

## Deliberately outside the first release

- VPS purchasing/provider APIs.
- Automatic censorship diagnosis or global server rotation.
- Custom native VPN clients or silent replacement of imported client configs.
- Kubernetes or a distributed service architecture.


Monitoring scope additions (2026-11-17): country flag and city for servers; traffic history by user (all devices), device (all servers), and device/server pair. Preserve measured history across profile rotations and migrations; expose sample age/gaps and avoid claiming that a recent handshake proves end-to-end connectivity. Detailed acceptance is in `health-notifications.md`.

## Health/push implementation checkpoint

Implemented: periodic isolated AWG DNS/HTTPS probe, debounced incidents/recovery, maintenance, role/audience-scoped inbox, automatic main-server-change reminders, manual outstanding-audience preview/send, final job-failure admin alerts, encrypted VAPID/Web Push with preferences and retries, PWA installation support, current-browser self-test and transport status. Monitoring CI passed at `90865df`. Health/push CI passed, the NAS was upgraded, server probes and desktop Chrome/Android push receipt were confirmed; Android status-bar badge acceptance is confirmed; iOS acceptance remains pending. Optional scheduled reminders and arbitrary selected-user campaigns remain extensions. WARP and VLESS remain future adapters; the local gateway is now implemented with acceptance in progress.

## Local gateway implementation in progress

The owner confirmed VMM support and chose fail-closed. Implementing an administrator-managed Ubuntu VM registered/deployed by password or saved SSH key from the portal, stable LAN WireGuard identity, independent AWG profile, profile-drift display and CI packet tests. See [local gateway](local-gateway.md). Runtime/CI passed at `f72d313`; desktop/mobile interactive UI acceptance passed; real VM/Keenetic acceptance is still pending.
