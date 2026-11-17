#!/bin/sh
set -eu
[ -f /run/cuchi-gateway-ready ]
# Check actual uplink DNS and HTTPS; binding the AWG source selects the
# tunnel-only policy table. Failure marks unhealthy and never enables WAN.
address=$(cat /run/uplink-address)
address=${address%/*}
dig -b "$address" @1.1.1.1 example.com A +short +time=3 +tries=1 | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$'
curl --interface "$address" --fail --silent --show-error --connect-timeout 3 --max-time 8 https://1.1.1.1/cdn-cgi/trace >/dev/null
