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

Implemented code includes steps 1–8; real VPS/official-client acceptance is still pending. Expiry, configurable grace periods and retirement controls remain future work.

Acceptance: admin registers a VPS and deploys it; a normal user generates two distinct working device profiles; another user cannot access either; reissue/retry creates no duplicates; withdrawal really removes the server peer.

## Health, profile status and browser reminders

Detailed requirements: [health and notifications](health-notifications.md). Monitor server-side operation only; no automatic user-network probes. Display profile currency separately from runtime updates and peer reconciliation. Critical severity uses ordinary Web Push with high transport urgency, without promising native iOS Critical Alerts.

- Manifest, service worker, VAPID, browser subscription lifecycle.
- Explicit consent and an iOS Home Screen installation guide.
- Admin preview/test/send, user selection and stale-profile audiences.
- Inbox, deduplication, bounded retries and optional scheduled reminders.
- Notification opens the authenticated device list; no secrets in payloads or URLs.

## Local gateway

- Linux VM preferred where supported; validate Xpenology virtualization/TUN before deciding.
- Ordinary LAN WG from Keenetic to the gateway, AWG from gateway to a VPS.
- Explicit direct WAN route for the tunnel transport and management traffic.
- DNS/IPv6/MTU tests and a user-chosen fail-open/fail-closed policy.

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
