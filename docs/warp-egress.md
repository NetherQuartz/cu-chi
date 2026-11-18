# Experimental WARP egress

Each ready VPS has an independent **Выход в интернет** control. Existing and new servers default to direct egress. Updating the NAS alone does not reconnect/redeploy running VPS containers. On the next explicit server/profile reconciliation the runtime can update, briefly resetting connections; profile contents remain unchanged.

An administrator enables WARP after acknowledging Cloudflare's application terms. The VPS registers a free WARP identity using **unofficial wgcf 2.3.1**, pinned to commit `de6bba7147dc965a1b8e4de113a709750697ccea`. The CI build pins Brotli 1.2.7 and klauspost/compress 1.20.2 to fix GO-2026-6888/GO-2026-6865 in upstream dependencies, and runs govulncheck before compiling. This is not the official Cloudflare Linux daemon and has no compatibility guarantee for future registration/API changes. There is no WARP+ license or Zero Trust account integration. Use a spare VPS for first acceptance; do not test on the family server. Cloudflare registration is not performed in CI.

The UI distinguishes requested mode from the last successfully applied mode. Switching runs through the existing SSH job queue. Repeated clicks during a pending same-mode operation are rejected. A queued opposite selection supersedes the desired generation; current work completes/rolls back before the next queued application. Failed jobs retry up to three times; failed WARP changes retain administrator alerts and actor names rather than being mistaken for old-server revocation cleanup.

## Traffic and failure behavior

AWG ingress, server endpoint, keys, address allocation and client files do not change. WARP changes the public exit IP for all profiles on that VPS, including an AWG uplink used by a local Keenetic gateway. Existing TCP/UDP sessions can reset. Device profiles and router configuration need no reissue. WARP does not fix a blocked VPS ingress, guarantee access to any service or guarantee a particular exit city.

Only forwarded IPv4 from `awg0` uses `warp0` in the container network namespace. Host SSH, Docker and AWG outer routing stay direct. Client DNS follows the same tunnel. MTU is 1280 for both inner interfaces; IPv6 remains disabled as in the original runtime. If WARP becomes unavailable, traffic stops: no automatic direct fallback. An administrator can explicitly choose direct egress again, which changes the exit IP and resets affected sessions.

Apply requires actual AWG data flow, DNS, HTTPS and a Cloudflare trace reporting `warp=on` or `warp=plus`. On failure, the script tries to restore the preceding egress mode and leaves the job failed/pending retry. A failed rollback is reported distinctly; the displayed applied mode is historical, not an assertion of current reachability. Check health and choose direct output/reapply when needed. Periodic probes also require WARP trace while WARP is selected on the runtime.

Kernel WireGuard must be available on the VPS (`modprobe wireguard`). Supported provisioner OS/architecture remain Ubuntu 24.04/26.04 amd64. Registration/handshake can be blocked by a provider, Cloudflare or a network firewall. No automatic endpoint scanning or third-party installation scripts are used.

## Credentials, backup and rollback

The registration helper has no Linux capabilities, a read-only image and a root-only bind mount; it is bounded, labelled and removed on exit. wgcf account/profile cache lives under `/opt/cu-chi/<server-id>/warp-registration` on that VPS. The validated profile is encrypted in PostgreSQL with the NAS master key and server-specific associated data. Standard database/master-key backup bundles include it. Neither account token nor private key is exposed in browser APIs or job messages. Disabling retains the identity for re-enable. Cloudflare-side account deregistration is not implemented.

Migration 014 is additive. Before rolling the app back to a release without egress support, explicitly disable WARP on every enabled VPS and wait for successful application. An old app cannot manage WARP; an old runtime can silently revert to direct on its next reconciliation. Restore drills must preserve master key, database and their associated runtime version. A clean VPS reinstall must also use the normal explicit SSH host-key replacement workflow.

## Verification and outstanding acceptance

- Go tests: restricted profile parsing, ownership/admin/CSRF through existing middleware, encrypted identity reuse, per-server isolation, desired/applied failure semantics and byte-identical client downloads.
- Embedded shell tests: successful apply, failed enable/disable rollback, missing kernel module, rollback failure, generation and temporary-secret cleanup.
- CI packet acceptance: AWG → isolated ordinary-WG exit → network, large TCP transfers, public UDP DNS/TLS, actual WG byte counters, peer/interface loss, direct disable/re-enable and restart with unchanged AWG identity. The test exit is deliberately reachable directly to detect fail-open bugs. The production probe rejects this substitute's `warp=off` response.
- Still required on a **spare VPS**: actual wgcf registration, `warp=on` trace, external native-client ingress, QUIC/UDP workloads, public IP change, Cloudflare failure/recovery and disable rollback. The owner currently has no spare VPS; production WARP is not enabled or verified.
