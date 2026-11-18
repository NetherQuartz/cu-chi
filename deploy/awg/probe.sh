#!/bin/sh
set -eu
amneziawg-go -f probe0 >/tmp/probe.log 2>&1 &
pid=$!
trap 'kill "$pid" 2>/dev/null || true' EXIT INT TERM
for i in $(seq 1 50); do [ -S /run/amneziawg/probe0.sock ] && break; sleep 0.1; done
awg setconf probe0 /config/probe.conf
ip address add 10.66.255.254/32 dev probe0
ip link set dev probe0 mtu 1280 up
ip route add 10.66.0.1/32 dev probe0
ip route add 1.1.1.1/32 dev probe0
# A successful handshake alone does not prove forwarding or DNS work.
for attempt in 1 2 3; do
 if dig @1.1.1.1 example.com A +short +time=5 +tries=1 | grep -Eq '^[0-9]+\.' && curl --fail --silent --show-error --connect-timeout 5 --max-time 15 https://1.1.1.1/cdn-cgi/trace > /tmp/trace; then
  if [ "${CUCHI_EXPECT_WARP:-direct}" != warp ] || grep -Eq '^warp=(on|plus)$' /tmp/trace; then exit 0; fi
 fi
 sleep 1
done
exit 1
