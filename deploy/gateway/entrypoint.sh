#!/bin/sh
set -eu
umask 077
if [ "${1:-}" = init ]; then exec /usr/local/bin/cuchi-gateway-config init; fi
[ "$#" -eq 0 ] || exit 2
# Dedicated container namespace only. Fail closed before any interface is up.
iptables -P FORWARD DROP
ip6tables -P FORWARD DROP
iptables -P INPUT DROP
iptables -A INPUT -i lo -j ACCEPT
iptables -A INPUT -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT
router=$(cat /config/lan/router-ip)
# Validate before passing a stored value to an option-taking CLI.
case "$router" in ''|*[!0-9.]*) echo 'Invalid router address' >&2; exit 1;; esac
iptables -A INPUT -i eth0 -s "$router/32" -p udp --dport 51820 -j ACCEPT
/usr/local/bin/cuchi-gateway-config prepare
address=$(cat /run/uplink-address)
ip route add unreachable default metric 32760 table 200
ip route add unreachable default metric 32760 table 201
ip rule add priority 100 iif wg0 lookup 200
ip rule add priority 101 iif awg0 lookup 201
ip rule add priority 102 from "$address" lookup 200
# Host WAN/default routes stay untouched: tunnel transport uses eth0/main.
ip link add wg0 type wireguard
wg setconf wg0 /config/lan/wg0.conf
ip address add 10.77.0.1/24 dev wg0
ip link set wg0 mtu 1280 up
ip route add default dev wg0 metric 10 table 201
mkdir -p /run/amneziawg
amneziawg-go -f awg0 &
pid=$!
cleanup() {
 trap - EXIT INT TERM
 iptables -P FORWARD DROP
 iptables -F FORWARD
 kill "$pid" 2>/dev/null || true
 ip link del wg0 2>/dev/null || true
 ip link del awg0 2>/dev/null || true
 rm -f /run/cuchi-gateway-ready
}
trap cleanup EXIT
trap 'exit 0' INT TERM
for i in $(seq 1 50); do [ -S /run/amneziawg/awg0.sock ] && break; sleep 0.1; done
awg setconf awg0 /run/uplink.conf
ip address add "$address" dev awg0
ip link set awg0 mtu 1280 up
ip route add default dev awg0 metric 10 table 200
# Only decrypted LAN -> AWG, and the established reverse path. No eth0 fallback.
iptables -A FORWARD -i wg0 -o awg0 -j ACCEPT
iptables -A FORWARD -i awg0 -o wg0 -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT
iptables -t nat -A POSTROUTING -o awg0 -j MASQUERADE
iptables -t mangle -A FORWARD -p tcp --tcp-flags SYN,RST SYN -j TCPMSS --clamp-mss-to-pmtu
touch /run/cuchi-gateway-ready
printf 'Cuchi gateway started; forwarding is restricted to WireGuard -> AmneziaWG\n'
wait "$pid"
