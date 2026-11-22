# Roadmap

## Foundation (implemented)

- OIDC and group-based roles; 30-minute idle sessions renewed by foreground interaction, not background polling.
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

- WARP real external AWG forwarding, UDP DNS/TLS, large transfers, restart and failure/rollback passed on a dedicated Ubuntu 26.04 VPS; QUIC-specific and native-client UI checks remain.
- Per-server direct/WARP policy is implemented, direct by default. NAS rollout is pending; no family VPS was switched.
- Add Xray/VLESS through a separate adapter and profile exporter.

## Deliberately outside the first release

- VPS purchasing/provider APIs.
- Automatic censorship diagnosis or global server rotation.
- Custom native VPN clients or silent replacement of imported client configs.
- Kubernetes or a distributed service architecture.


Monitoring scope additions (2026-11-17): country flag and city for servers; traffic history by user (all devices), device (all servers), and device/server pair. Preserve measured history across profile rotations and migrations; expose sample age/gaps and avoid claiming that a recent handshake proves end-to-end connectivity. Detailed acceptance is in `health-notifications.md`.

## Health/push implementation checkpoint

Implemented: periodic isolated AWG DNS/HTTPS probe, debounced incidents/recovery, maintenance, role/audience-scoped inbox, automatic main-server-change reminders, manual outstanding-audience preview/send, final job-failure admin alerts, encrypted VAPID/Web Push with preferences and retries, PWA installation support, current-browser self-test and transport status. Monitoring CI passed at `90865df`. Health/push CI passed, the NAS was upgraded, server probes and desktop Chrome/Android push receipt were confirmed; Android status-bar badge acceptance is confirmed; iPhone push receipt is now confirmed by the owner. Optional scheduled reminders and arbitrary selected-user campaigns remain extensions. WARP and VLESS remain future adapters; the local gateway is now implemented with acceptance in progress.

## Local gateway implementation in progress

The owner confirmed VMM support and chose fail-closed. Implementing an administrator-managed Ubuntu VM registered/deployed by password or saved SSH key from the portal, stable LAN WireGuard identity, independent AWG profile, profile-drift display and CI packet tests. See [local gateway](local-gateway.md). Runtime/CI passed at `f72d313`; desktop/mobile interactive UI acceptance passed; real VM deployment/DNS/HTTPS passed after the cold-start fix; Keenetic household routing acceptance remains pending.


2026-11-18 field feedback: owner confirmed successful server replacement and gradual profile reissuance. Follow-up separates informational unreachable old-server cleanup from current profile failures and adds initiator context to administrator operation notifications. Does not introduce server retirement, automatic deletion or grace periods; real revocation remains required. Publication/rollout status is recorded in handoff.


2026-11-18 owner acceptance update: ordinary internet routing through the real Keenetic gateway and iPhone push receipt are confirmed. The owner did not separately enumerate VM/VPS-loss, DNS or IPv6 failure tests, so those specific assertions remain unverified. Restoration from the actual NAS backup has not been rehearsed. WARP remains an assessment/design topic; no deployment or activation is authorized by the effort/impact question alone.

2026-11-18 WARP implementation: owner authorized a per-server on/off control. Experimental direct/WARP desired/applied state, encrypted cached registration, container-only policy routing, fail-closed behavior, apply rollback and WARP-aware health checks are implemented. No spare VPS exists; Cloudflare registration and real WARP forwarding/QUIC remain acceptance gates. No production egress was changed. Isolated kernel-WG packet CI tests are separate from Cloudflare acceptance.

WARP implementation verification completed in CI 44290879338 (`6eac641`): isolated kernel-WG routing/failure/recovery tests and ordinary AWG/gateway regression suites passed; images published. Real Cloudflare registration, native-client WARP egress/QUIC and actual-VPS rollout remain pending a spare server. NAS has not yet been updated to this release.

Real WARP acceptance completed on an owner-supplied dedicated VPS (2026-11-18): published `6eac641` runtime registered with Cloudflare; external AWG traffic reported warp=on with a changed public IP and unchanged client profile. UDP DNS, HTTPS/128 KiB transfer, restart, interface-loss fail-closed/recovery and deliberate failed-enable rollback passed. No code fix or new runtime release was needed. Owner confirms NAS is not yet updated. Test deployment was removed so the VPS can be registered normally in the portal; original credentials are preserved. See warp-egress.md for exact coverage and remaining QUIC/native-client UI/real NAS-toggle limits.


Xray VLESS/Vision + REALITY with automatic target selection, per-device subscriptions and independent VPS deployment is implemented. The owner confirmed the earlier WARP rollout through the NAS portal.

Administrator server editing and retirement are implemented. The same VPS can change address or login credentials, and retired servers keep labelled history. Automatic provider-side decommissioning and timed profile overlap remain outside this feature.
