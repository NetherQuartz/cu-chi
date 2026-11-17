# Health, profile status and notifications — next milestone

Status: requirements agreed on 2026-11-17. Resource/traffic sampling, dashboards, location and basic service status are implemented locally in the monitoring milestone; NAS rollout is pending. Periodic full tunnel probes, incidents and notifications are still pending. The owner does not want automatic probes from users' networks. Keep the existing manual import check for the first real client only.

## Separate the facts shown in the portal

1. **Server deployment:** desired/applied state and the last successful deployment. This already exists; it is not a continuous health check.
2. **Server health:** SSH reachability, AWG process/interface, and a temporary test peer performing DNS and HTTPS egress. Display the time and vantage point of the check. This proves operation from the monitoring system/VPS, not reachability from every ISP.
3. **Profile currency:** whether the issued revision matches its service connection settings and whether it uses the current main server. Never infer successful client import from downloading or generating a profile.

The user-facing server card should show a friendly name, optional administrator-supplied region, AWG version, health summary, last check and whether action is needed for each device. Suggested states: «Профиль актуален», «Доступен новый сервер — перевыпустите профиль», «Профиль применяется», «Профиль отозван», «Есть проблема с сервером», «Нет свежих данных». Link action-required states directly to the affected device. Detailed SSH/Docker errors and infrastructure diagnostics belong in the administrator view.

Keep three version concepts distinct:

- Software/runtime version: changing the container alone need not require new client profiles.
- Connection revision: changes to endpoint, server identity, obfuscation or client-relevant settings can invalidate profiles.
- Reconciliation generation: adding another user's peer changes the generation but must never mark everyone else's profile obsolete.

Selecting a new main server creates a migration recommendation for devices bound elsewhere. It does not itself revoke old profiles. An administrator may later mark an old service as retired/reissue-required with a reason; this needs an explicit operation and audit event, not a health-check side effect.

## Monitoring behavior

Proposed initial defaults: lightweight checks every minute; full tunnel/DNS/HTTPS checks every five minutes; jitter and bounded timeouts. Serialize probes against deployment/revocation, use a fresh temporary peer and always remove it. Prefer read-only checks between full probes; monitoring must never replace user peers, restart services or rotate profiles automatically.

Store samples and incident transitions separately from deployment jobs. Three consecutive failures open an incident; two successes close it. Show «проверяем» after a first failure, and «нет свежих данных» when samples are stale. Distinguish SSH-only failure from a confirmed data-plane failure. Planned maintenance and deployment suppress routine outage alerts. If all targets fail together, consider control-plane/network failure before attributing independent VPS outages.

No automated client-network probe or censorship diagnosis is planned. A NAS-hosted monitor cannot send an alert while the NAS itself or its internet connection is down; an optional external watchdog would be a separate future feature.

## Recipients and noise control

| Event | Administrators | Ordinary users |
|---|---|---|
| Sustained AWG/data-plane outage | Incident details and affected services | Only users with profiles on that service; concise notice |
| SSH management unavailable but VPN not proven broken | Yes | No automatic outage claim |
| Failed deployment/reconciliation/revocation | Yes, actionable reason | Only affected devices; explain whether to wait or contact admin |
| Main server replaced / reissue required | Summary and outstanding-device count | Only owners of outdated affected profiles, with a link to their devices |
| Recovery | One recovery event | Only recipients previously notified of that incident; preference-controlled |
| Routine successful check, deployment internals | Dashboard/audit | No push |
| Manual reminder | Preview audience and confirm sending | Selected users or outstanding-profile audience |

Aggregate by user and incident so several devices do not produce duplicate pushes. Send on incident transitions, apply a cooldown for continuing outages, coalesce queued messages, expire stale outage notices, and suppress a pending outdated-profile reminder if the user has already reissued. Preserve an in-portal inbox even when push is denied or unavailable. Users choose notification categories; administrators have additional operational categories.

## Severity and platform limits

Use application-level `info`, `warning` and `critical` severity, with clear wording and accessible visual treatment. Critical VPN events use Web Push `Urgency: high` and an appropriate TTL; this requests prompt transport delivery, not a bypass of the device's sound or Focus settings. Apple documents these standard Web Push headers in [Sending web push notifications](https://developer.apple.com/documentation/usernotifications/sending-web-push-notifications-in-web-apps-and-browsers).

**Do not promise native iOS Critical Alerts through a website.** Those alerts use an Apple-approved entitlement and separate authorization for a native app; the standard browser Notifications API has no corresponding critical-alert option. See [Apple's Critical Alerts entitlement](https://developer.apple.com/documentation/bundleresources/entitlements/com.apple.developer.usernotifications.critical-alerts) and the [Notifications API standard](https://notifications.spec.whatwg.org/).

On iOS/iPadOS, install the portal on the Home Screen and request notification permission only after a user gesture. Notifications respect Focus settings; provide setup guidance instead of claiming forced delivery. See [WebKit's Web Push guidance](https://webkit.org/blog/13878/web-push-for-web-apps-on-ios-and-ipados/). Android presentation and sound also depend on browser/OS notification settings; user-controlled channels can govern interruption behavior ([Android notification documentation](https://developer.android.com/develop/ui/compose/notifications)). The portal cannot guarantee an audible/head-up alert on every device.

## Delivery and security

Use VAPID and a durable transactional notification outbox with bounded retries/backoff. Subscription endpoints and keys belong to a user/browser installation, not to a VPN device. Validate subscription destinations and prevent SSRF; never permit arbitrary URLs or private network endpoints. Remove expired subscriptions after terminal delivery responses. Recheck authorization and audience membership at send time.

Payloads contain an event identifier, generic title/body and same-origin navigation target only. Never include private keys, profiles, SSH credentials, public VPS IPs or sensitive diagnostic details. A push-service acceptance response means accepted for delivery, not received/read/imported. Service workers must display received push events and must not cache authenticated API responses or profile files.

Acceptance includes real iOS Home Screen and Android browser subscriptions, denied permission, expired subscription, offline delivery/expiry, deduplication, recovery suppression, correct per-user targeting and administrator-only operational alerts.

## Per-server resource dashboard (owner reference, 2026-11-17)

For administrators, provide five compact metric cards with current values and real time-series charts: outgoing/incoming network rates, CPU usage/core count, RAM used/total, and root filesystem used/total. Show sent/received totals with an explicit counter period, plus a selectable history interval (initially hour/day). Follow the existing Củ Chi design rather than copying the reference's theme.

Collect host-level metrics server-side. Network rates should use the VPS external/default-route interface and monotonic byte deltas; exclude Docker/veth/loopback to avoid double counting. Clearly label traffic as VPS interface traffic (includes management/probe/other traffic), not per-user VPN consumption. CPU should use interval deltas with correct idle/iowait accounting; memory should use MemAvailable. Handle reboots, interface changes and counter resets with gaps/reset markers instead of negative rates. Time-series retention and sampling are bounded. No fabricated initial chart data.

Display collection time, sample age and stale/unavailable state. Missing data is not zero utilization and does not prove VPN failure. Charts should preserve gaps. Keep host addresses, detailed resource metrics and infrastructure failures admin-only; ordinary users receive the separate summarized health/profile-currency view. This dashboard is planned, not yet implemented.


## Per-device traffic and connection history (owner reference, 2026-11-17)

The owner requested client statistics both aggregated by device across all servers and split by device/server pair. Show upload/download totals, recent rates and real history charts; last successful handshake; assigned tunnel address; and the server/location. User-facing directions are from the device's perspective (server peer RX is device upload, peer TX is device download). Never sum these as host-interface totals: encapsulation, management and probe traffic differ. For a gateway, traffic represents the whole routed LAN, not individual LAN clients.

Persist stable device and server identifiers independently of profile/key generations. Accumulate nonnegative deltas per peer incarnation; retain already-observed usage after key rotation, revocation, container restart and server replacement. A counter decrease or missing baseline starts a new segment, not negative traffic or a fabricated rate. Use idempotent sample writes and serialized polling to prevent duplicate accounting. Final reconciliation should collect available counters before peer removal; abrupt removal/reboot and collection gaps can lose unobserved bytes. Label totals as measured since monitoring began and show gaps; do not promise billing-grade accuracy. Define behavior for restored databases and disappeared/reappearing peers explicitly.

Views: user overview summing all their devices, device overview across servers, device/server detail including historical profile revisions, and administrator server peer list. The owner explicitly requested user-level totals too: show upload/download, history and device/server breakdown for the selected interval, counting each observed byte delta once. Users can view only their own aggregate; administrators can select any user. Preserve attribution to the owner at measurement time if device transfer is ever introduced; do not accidentally expose a prior owner’s history. Filters: hour/day/week/month and server; sorting by traffic, most recent connection and action needed. Useful additions: affected-profile counts when retiring a server, history of reissues, and a clear distinction between a currently selected profile and an older retained connection record. Owners see only their own devices; administrators see all. No sites visited, DNS query histories or content inspection. Client public endpoint addresses are unnecessary for the initial user view; detailed diagnostics, if later added, require bounded retention and administrator access.

Fresh handshake means recent tunnel activity, not proven end-to-end internet access. Inactive/mobile sleeping peers are not outages; no alerts merely because a device is quiet. Stale sampling is a separate state. Do not run browser/client-network probes. Counter sampling should be read-only, separate from the occasional full synthetic health probe, and exclude synthetic probe peers. Bound raw retention and roll up older samples; initial proposal: 24h raw, 30d minute/hour summaries and 12mo daily totals, with gaps preserved. Exact defaults remain an implementation choice to validate against expected scale.

Acceptance: profile rotation and server migration retain aggregate history; peer reset/reboot produces no spikes or negative rates; retry cannot double count; device/server sums reconcile with observed device totals; missed intervals show gaps; user isolation includes historical samples; sleeping devices do not trigger outage pushes.

## Server location (owner request, 2026-11-17)

Show country flag plus localized country name and optional city beside the friendly server name, in both administrator cards and users' assigned-server/profile details. Store an ISO 3166-1 alpha-2 country code and a bounded plain-text city; allow an unset location. The flag must have a textual equivalent and should use self-hosted SVG assets for consistent rendering on Windows and mobile. Do not rely on emoji flag support alone. Validate/escape city input; never insert it as HTML.

Country and city are administrator-entered metadata, editable without redeploying or invalidating any profiles. Optional IP geolocation can suggest a value later, explicitly approximate and always overridable; it is not a health signal and should not require a third-party request on every page view. The owner confirmed both country flag and city are desired. Planned with the monitoring dashboard, not implemented in the current release.


Implementation retention choice: this milestone keeps minute-level host/traffic details for 30 days and lifetime observed pair totals, rather than introducing multi-level 12-month rollups immediately. The first observation is a baseline, never retrospective usage. The dashboard provides hour/day resource charts and hour/day/week/30-day traffic periods; polling yields to deployment work. Collection failures and old samples are explicitly shown. Recurring full-tunnel probes and push incidents remain pending.

## Health and notification implementation (2026-11-17)

Implemented in code, NAS/native-device acceptance pending:

- Every five minutes on deployed, reconciled, idle servers, the existing pinned SSH worker adds a fresh temporary AWG peer and runs the shipped DNS/HTTPS probe in a separate namespace. It reuses the installed digest, does not pull/restart/reconcile user peers, shares the remote lock, checks the generation and removes its synthetic peer before reporting success. A public-key journal recovers interrupted cleanup. This tests local VPS tunnel/NAT/egress, not provider inbound UDP or a censored user network.
- Three consecutive failures of the same category open an incident; two successes close it. Gaps over 15 minutes reset consecutive counts. SSH/pin/configuration failures notify administrators only. Data-plane failures notify administrators and owners of active profiles on that server. An escalation from management failure to confirmed VPN failure has its own incident. Quiet client devices never trigger incidents.
- Administrators can pause probes and outage pushes during maintenance (UI one hour, API up to one day). Deployments take priority; queued/running jobs and generation mismatches suppress probe results. Dashboard shows checking/stale/incident/maintenance separately from profile currency.
- A durable per-user inbox stores the last 90 days, showing the most recent 100 events. Each actual default-server change creates a fresh reminder for affected users, with no daily limit; repeated selection of the unchanged default does not create an event. New explicit manual sends are also unlimited by day. Retries of the same action are idempotent for 90 days. Administrators preview and resend to the currently outstanding audience; reissued profiles are excluded again before push. Final job failure notifies administrators. Successful routine jobs/checks do not send pushes.
- Preferences control outage/recovery/reminder/operations pushes; inbox remains available. Recovery pushes require prior push-service acceptance of the outage for that user, while recovery inbox entries go to original incident recipients. There are no repeated ongoing-outage reminders. Automatic scheduled profile reminders and arbitrary selected-user campaigns are not implemented.
- VAPID identity is generated once and encrypted in PostgreSQL using the existing master key; no new environment secret is necessary. Subscriptions are independently encrypted and owner-bound, limited to 20 per user. Endpoints never appear in inbox/API status/logs. Known Google FCM, Apple and Mozilla endpoints are allowed; HTTPS only, no credentials/redirects/proxy, DNS addresses verified public and dialed without a second lookup. Other providers currently require a reviewed allowlist extension.
- Independent durable push queue: claims with expiring leases, at most five attempts, bounded exponential backoff, deletion of expired subscriptions (404/410), recheck roles/preferences/audience/incident/expiry/read status before each send. Stable notification tags coalesce retries; exactly-once external delivery cannot be guaranteed. Provider acceptance is explicitly not receipt/read/import. Outage transport TTL is five minutes; other pushes at most one hour (test expires after five minutes). The NAS cannot alert while it is offline.
- PWA manifest and raster icons, a service worker with no fetch handler or caches, same-origin inbox navigation, explicit user-gesture permission, iOS Home Screen guidance. Critical severity requests Web Push high urgency but cannot override device Focus/sound policies.
- “Send test to myself” is restricted to the current user's current browser subscription, with one test per subscription/minute. Other browsers on the same account and other users receive nothing. UI reports queued/sending/accepted/failed/expired without claiming display on the device. Subscription restoration has an explicit reconnect action.

Acceptance still needed after NAS update: real Safari Home Screen iOS, Android browser and desktop permission/subscription/test notification, real background delivery and click-to-login, current VPS periodic probe and maintenance observation. Local mock-transport tests prove queue/ownership/crypto/error behavior, not real push delivery. CI additionally runs real AWG add/remove synthetic-peer traffic while a separate existing client continues transferring.
