# A configured interface is not yet a usable tunnel. Allow bounded warm-up
# without restarting it (and resetting the handshake) on every failed probe.
wait_for_gateway_egress() {
 for health_attempt in $(seq 1 6); do
  if health_result=$(docker exec cuchi-gateway /usr/local/bin/cuchi-gateway-healthcheck 2>/dev/null); then return 0; fi
  if [ "$health_attempt" -lt 6 ]; then sleep 2; fi
 done
 # Only fixed stage codes may enter application logs/state. Never forward raw
 # output, which could contain addresses or other deployment details.
 case "$health_result" in
  CUCHI_ERROR_gateway_dns_failed) echo CUCHI_ERROR_gateway_dns_failed;;
  CUCHI_ERROR_gateway_https_failed) echo CUCHI_ERROR_gateway_https_failed;;
  CUCHI_ERROR_runtime_start_failed) echo CUCHI_ERROR_runtime_start_failed;;
  *) echo CUCHI_ERROR_gateway_egress_failed;;
 esac
 return 23
}
