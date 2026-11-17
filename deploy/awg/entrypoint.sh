#!/bin/sh
set -eu
umask 077
mkdir -p /run/amneziawg
amneziawg-go -f awg0 >/tmp/awg.log 2>&1 &
pid=$!
trap 'kill "$pid" 2>/dev/null || true; exit' INT TERM EXIT
for i in $(seq 1 50); do [ -S /run/amneziawg/awg0.sock ] && break; sleep 0.1; done
awg setconf awg0 /config/awg0.conf
ip address add 10.66.0.1/16 dev awg0
ip link set dev awg0 mtu 1280 up
# These rules only affect this container's dedicated network namespace.
iptables -A FORWARD -i awg0 -o eth0 -s 10.66.0.0/16 -j ACCEPT
iptables -A FORWARD -i eth0 -o awg0 -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT
iptables -P FORWARD DROP
iptables -t nat -A POSTROUTING -s 10.66.0.0/16 -o eth0 -j MASQUERADE
touch /run/cuchi-ready
wait "$pid"
