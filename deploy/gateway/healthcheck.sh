#!/bin/sh
set -eu
[ -f /run/cuchi-gateway-ready ] || { echo CUCHI_ERROR_runtime_start_failed; exit 1; }
# Check actual uplink DNS and HTTPS; binding the AWG source selects the
# tunnel-only policy table. Failure marks unhealthy and never enables WAN.
address=$(cat /run/uplink-address)
address=${address%/*}
if ! dig -b "$address" @1.1.1.1 example.com A +short +time=3 +tries=1 2>/dev/null | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$'; then
 echo CUCHI_ERROR_gateway_dns_failed; exit 1
fi
if ! curl --interface "$address" --fail --silent --show-error --connect-timeout 3 --max-time 8 https://1.1.1.1/cdn-cgi/trace >/dev/null 2>&1; then
 echo CUCHI_ERROR_gateway_https_failed; exit 1
fi
