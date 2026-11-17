# VPS deployment and profiles

## Supported target

Use a dedicated Ubuntu 24.04 or 26.04 LTS **amd64** VPS with public IPv4, TUN (`/dev/net/tun`), outbound HTTPS/DNS and root SSH or unrestricted non-interactive sudo. Allow the SSH port and chosen VPN UDP port in the provider firewall. Active UFW is rejected: Docker port publishing has its own forwarding behavior. Existing firewalls, Docker workloads and custom routing are outside this first supported target.

A small family deployment can start with 1–2 vCPU, 1–2 GB RAM and 10–20 GB disk; actual throughput depends on the VPS. The NAS needs no TUN, Docker socket or VPN privileges.

Choose an SSH access method in **Серверы**:

- **Сохранённый ключ**: use **Добавить ключ** to generate Ed25519 or import an existing private key without a passphrase. Copy its public part into the provider's SSH-key selection at VPS creation, or into `~/.ssh/authorized_keys` for the selected VPS account via a trusted console. Select this saved key on each new server. The NAS keeps the encrypted private part and never returns it to the browser. Keys from your workstation/SSH agent are not used automatically.
- **Отдельный приватный ключ**: the key belongs only to that server record; existing registrations keep working unchanged.
- **Пароль**: enter the existing SSH account password. Password authentication must already be allowed by the VPS's sshd policy (including root policy when applicable). Củ Chi does not change that policy. Standard SSH password authentication is supported; interactive MFA/keyboard-interactive and password-based sudo are not. A non-root account still needs unrestricted passwordless sudo.

Reusing a key is convenient for replacing VPSs; separate keys remain possible. A saved key cannot be deleted while a server references it. Deleting an unused key only removes the portal record, not public keys on remote hosts. Reassigning/rotating credentials for an existing server is not yet implemented. Retain database and master-key backups because generated private keys are not exported.

By default, **Запомнить при первом подключении** discovers the host key without sending a password/private-key authentication, then persists its SHA-256 fingerprint when registering the VPS. All jobs verify that saved fingerprint. This TOFU mode does not independently verify the initial host; changes later are rejected, never auto-accepted. For independent first-connection verification, select **Указать проверенный отпечаток** and obtain the **host fingerprint** from a trusted provider console (different from the public login key):

```sh
ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub -E sha256
```

Supply the `SHA256:…` value to Củ Chi. A key collected over the same untrusted connection is not independent verification; that is the explicit tradeoff of TOFU. Host-key mismatch aborts before remote commands. Keep credentials out of Git and chat transcripts.

## Administrator workflow

1. Add the server and SSH credentials in **Серверы**.
2. **Проверить SSH** checks authentication, pinned host identity, OS, architecture, root/sudo, TUN and UFW. It does not deploy software.
3. **Развернуть** asks for the public endpoint and UDP port (default 51820). They become immutable for this server record. The job installs Ubuntu's `docker.io` package if needed and starts Docker.
4. The worker pulls the CI-pinned AWG runtime digest, creates an owned Docker network and container, and stores root-only configuration under `/opt/cu-chi/<server-id>`. It refuses unrelated containers/networks with the same names. Docker publishes the UDP port; forwarding and masquerading inside the VPN container allow IPv4 egress. Existing host rules are not flushed.
5. A temporary peer tests encrypted traffic, DNS and HTTPS egress. The temporary key is removed, the real peer list is verified, and only then is the job marked successful. A failure remains visible and retries up to three attempts, 30 seconds apart. **Повторить применение / Развернуть** queues a manual retry without rotating keys.
6. Select **Сделать основным**. This changes the target for future profile issuance, leaving current profiles intact.

The self-test uses a separate network namespace on the VPS; it cannot prove that a provider firewall admits inbound UDP or that the address is reachable from a user's network. Import a real client profile and test from outside the VPS. DNS/HTTPS probes currently depend on Cloudflare `1.1.1.1`; their failure prevents readiness even if the tunnel itself works.

## User workflow

Add each iOS/Android/Windows/macOS device separately, then choose **Выпустить профиль**. Download `.conf` or scan the QR only after the peer is installed. Import into a compatible AmneziaWG 3.1+ application. A repeated download returns the same keys; **Перевыпустить** creates a new revision and revokes the old one. Do not share one profile between independently connecting devices. A future routing gateway will represent one tunnel endpoint.

Selecting a replacement main server does not force users to migrate. They manually reissue their device profiles. On reissue the old profile is immediately scheduled for removal, so there is **no grace period**. If the old VPS is unreachable, its revocation stays pending while a profile on the new VPS can become ready. Unreachable machines may still accept their old keys: removal is confirmed only after reconnecting and applying the desired state. Destroy an abandoned VPS at the provider when it is no longer needed.

**Отозвать** schedules removal of every remaining profile for that device. Deleting the device is blocked until all servers confirm removal. A failed job leaves an administrator-attention notice; retry on the affected server. No server retirement/force-abandon control is implemented yet.

IPv4 uses a full tunnel. `::/0` is also included to prevent ordinary IPv6 traffic bypassing the tunnel; the runtime currently provides no IPv6 egress. Verify actual DNS/IPv6 routing on each client platform. Browser/OS traffic rules and application exceptions are outside the portal's control.

QR and configuration files contain private keys. Downloads are authenticated, ownership checked and marked `no-store`; do not post screenshots of them. Client import/connection state is not reported to the portal.

## Runtime and delivery

- [Official AmneziaWG Go source](https://github.com/amnezia-vpn/amneziawg-go/tree/v3.1.20260828): `b5928efb6ca19f0153958460c3d141f04abc5c2e`.
- [Official AmneziaWG tools source](https://github.com/amnezia-vpn/amneziawg-tools/tree/v3.1.20260812): `ee0f0a9aa34ff0a0da4b3433b9512781cfe02843`.
- `deploy/awg/Dockerfile` verifies source commits, builds with Go 1.26.11, scans reachable Go dependencies and includes upstream licenses. Tools accept `RandomTrailers = 1`.
- CI builds/tests/publishes `ghcr.io/netherquartz/cu-chi-awg:sha-<commit>` for amd64. The portal is then built for amd64/arm64 with that runtime's immutable digest embedded. Both packages must permit anonymous pulls. No build or GitHub token is needed on the NAS/VPS.
- `CUCHI_AWG_IMAGE` is an optional development/operator override restricted to this package with a SHA-256 digest. An ordinary local build has no default runtime and rejects deploy requests until one is provided. Published application images include it.
- Updating the NAS image updates the runtime selected by subsequent jobs. Existing VPS containers keep their current image until a reconciliation/deploy runs; that job may recreate the managed container if its image digest differs. Schedule updates accordingly.

## Persistence and recovery

Migrations 006–009 add measured traffic, health incidents, notifications and encrypted Web Push state. These are additive; older images do not collect/send the new features. Preserve the database and master key together, including the VAPID identity, and reconnect browser subscriptions if restoring an older identity. Migration `002_vpn.sql` adds profiles, job state and the main-server preference. Migration `004_host_key_policy.sql` records TOFU/manual provenance. Migration `003_ssh_access.sql` adds reusable SSH keys and password authentication; old server ciphertext and associated data stay intact. The legacy `encrypted_private_key` column stores an encrypted key or password according to `ssh_auth_method`. Do not downgrade the application after using saved keys/passwords: older workers cannot authenticate those records. Back up PostgreSQL and the unchanged master key before upgrading. Do not roll back to the inventory-only application after profiles are issued: it cannot manage or revoke them. Restore a matched database/application backup deliberately, then reconcile VPS state; restoring a database alone does not undo remote peers.

Keys and addresses are allocated transactionally before SSH. Retries reuse them. The worker holds a database session leadership lock; remote operations are serialized with `flock`, and monotonically increasing generations fence stale writes. Per-attempt tokens reject late completions. A restarted worker recovers unfinished jobs. Snapshot completion cannot turn a concurrently revoked profile back into a downloadable profile.

VPS configuration persists independently of NAS availability and Docker restarts. Keep the NAS master key and database backed up: losing either prevents managing existing identities. Server and profile private material is encrypted with record-bound AES-GCM; compromise of the running control plane remains compromise of its managed servers.

## Verification boundaries

Automated tests cover API ownership/CSRF, encryption, retries, concurrent revocation, server migration, SSH host-key rejection/cancellation, real AWG packet transfer, peer reconciliation and revocation. CI exercises the Linux runtime. Real VPS provisioning and imports in the four official client applications require separate acceptance checks; do not infer those from container tests.

Periodic health checks, inbox and Web Push are implemented; NAS rollout and real mobile push acceptance are pending. Grace periods, automated server retirement, WARP, ordinary-WG gateway and VLESS remain future work.
