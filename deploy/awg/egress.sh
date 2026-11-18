#!/bin/sh
set -eu
mode="$1"
case "$mode" in direct|warp) ;; *) exit 2;; esac
# Guard forwarding throughout the switch. A failed command leaves the guard in
# place; never fall through to an old direct rule after selecting WARP.
iptables -I FORWARD 1 -i awg0 -j DROP
iptables -N CUCHI-EGRESS 2>/dev/null || true
iptables -F CUCHI-EGRESS
iptables -C FORWARD -i awg0 -j CUCHI-EGRESS 2>/dev/null || iptables -A FORWARD -i awg0 -j CUCHI-EGRESS
# The original direct rule used to precede our chain on existing images. Keep
# the chain first, behind the temporary guard, without accumulating duplicates.
iptables -D FORWARD -i awg0 -j CUCHI-EGRESS
iptables -I FORWARD 2 -i awg0 -j CUCHI-EGRESS
if [ "$mode" = warp ]; then
 test -s /config/warp.conf
 address=$(cat /config/warp-address)
 ip route replace unreachable default metric 32760 table 200
 ip rule show | grep -q '100:.*iif awg0.*lookup 200' || ip rule add pref 100 iif awg0 lookup 200
 ip link show warp0 >/dev/null 2>&1 || ip link add warp0 type wireguard
 wg setconf warp0 /config/warp.conf
 ip addr replace "$address" dev warp0
 ip link set warp0 mtu 1280 up
 ip route replace default dev warp0 metric 10 table 200
 iptables -t nat -C POSTROUTING -s 10.66.0.0/16 -o warp0 -j MASQUERADE 2>/dev/null || iptables -t nat -A POSTROUTING -s 10.66.0.0/16 -o warp0 -j MASQUERADE
 iptables -C FORWARD -i warp0 -o awg0 -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT 2>/dev/null || iptables -A FORWARD -i warp0 -o awg0 -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT
 iptables -A CUCHI-EGRESS -s 10.66.0.0/16 -o warp0 -j ACCEPT
else
 while ip rule del pref 100 iif awg0 lookup 200 2>/dev/null; do :; done
 ip link del warp0 2>/dev/null || true
 iptables -A CUCHI-EGRESS -s 10.66.0.0/16 -o eth0 -j ACCEPT
fi
iptables -A CUCHI-EGRESS -j DROP
previous=$(cat /run/cuchi-egress-mode 2>/dev/null || echo direct)
if [ "$previous" != "$mode" ]; then conntrack -D -s 10.66.0.0/16 >/dev/null 2>&1 || true; fi
# Remove all guards left by an earlier interrupted switch, only after the final
# policy is in place. Locally originated SSH/WARP/AWG transport uses main routes.
while iptables -D FORWARD -i awg0 -j DROP 2>/dev/null; do :; done
printf '%s\n' "$mode" > /run/cuchi-egress-mode
