# Health, profile status and notifications — next milestone

Status: requirements agreed on 2026-11-17; not implemented by the VPS/profile release. The owner does not want automatic probes from users' networks. Keep the existing manual import check for the first real client only.

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
