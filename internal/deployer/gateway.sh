set -eu
umask 077
gateway_id="$1"; image="$2"; lan_ip="$3"; generation="$4"
exec 9>/run/lock/cuchi-gateway.lock
flock -w 900 9
base="/opt/cu-chi-gateway/$gateway_id"
# The VM must own this address; bind only its LAN interface, never all interfaces.
ip -4 -o addr show | awk '{print $4}' | cut -d/ -f1 | grep -Fxq "$lan_ip" || { echo CUCHI_ERROR_gateway_lan_ip_missing; exit 20; }
modprobe wireguard || { echo CUCHI_ERROR_wireguard_module_missing; exit 21; }
if ! command -v docker >/dev/null; then
 export DEBIAN_FRONTEND=noninteractive
 apt-get -o DPkg::Lock::Timeout=120 update >/dev/null
 apt-get -o DPkg::Lock::Timeout=120 install -y docker.io ca-certificates >/dev/null
fi
systemctl enable --now docker >/dev/null
docker info >/dev/null
# A gateway VM is separate from VPS ingress; reject accidental wrong-host deploys.
if docker inspect cuchi-awg >/dev/null 2>&1; then echo CUCHI_ERROR_gateway_dedicated_vm_required; exit 22; fi
if docker inspect cuchi-gateway >/dev/null 2>&1; then
 [ "$(docker inspect -f '{{ index .Config.Labels "io.cuchi.gateway" }}' cuchi-gateway)" = "$gateway_id" ] || { echo CUCHI_ERROR_foreign_container; exit 15; }
fi
install -d -m 700 "$base" "$base/config" "$base/config/lan"
if [ -f "$base/generation" ] && [ "$(cat "$base/generation")" -gt "$generation" ]; then echo CUCHI_ERROR_stale_generation; exit 16; fi
stage=$(mktemp -d "$base/stage.XXXXXX")
trap 'rm -rf "$stage"' EXIT
tar --no-same-owner -xf - -C "$stage"
docker pull "$image" >/dev/null
# Validate the profile before replacing the running gateway.
docker run --rm --cap-drop ALL --read-only --tmpfs /run -v "$stage:/config:ro" --entrypoint /usr/local/bin/cuchi-gateway-config "$image" prepare >/dev/null
if ! docker network inspect cuchi-gateway >/dev/null 2>&1; then docker network create --label "io.cuchi.gateway=$gateway_id" cuchi-gateway >/dev/null; fi
[ "$(docker network inspect -f '{{ index .Labels "io.cuchi.gateway" }}' cuchi-gateway)" = "$gateway_id" ] || { echo CUCHI_ERROR_foreign_network; exit 17; }
printf '%s\n' "$generation" > "$base/generation.new"
mv "$base/generation.new" "$base/generation"
install -m 600 "$stage/wg0.conf" "$base/config/lan/wg0.conf"
install -m 600 "$stage/router-ip" "$base/config/lan/router-ip"
install -m 600 "$stage/uplink.conf" "$base/config/uplink.conf.new"
mv "$base/config/uplink.conf.new" "$base/config/uplink.conf"
if docker inspect cuchi-gateway >/dev/null 2>&1; then docker rm -f cuchi-gateway >/dev/null; fi
docker run -d --name cuchi-gateway --label "io.cuchi.gateway=$gateway_id" --restart unless-stopped --network cuchi-gateway \
 --cap-drop ALL --cap-add NET_ADMIN --device /dev/net/tun --security-opt no-new-privileges \
 --read-only --tmpfs /run --tmpfs /tmp --sysctl net.ipv4.ip_forward=1 \
 --sysctl net.ipv4.conf.all.rp_filter=0 --sysctl net.ipv4.conf.default.rp_filter=0 --sysctl net.ipv6.conf.all.disable_ipv6=1 \
 --log-driver json-file --log-opt max-size=10m --log-opt max-file=3 \
 --health-cmd /usr/local/bin/cuchi-gateway-healthcheck --health-interval 60s --health-timeout 15s --health-retries 3 --health-start-period 30s \
 -p "$lan_ip:51820:51820/udp" -v "$base/config:/config:ro" "$image" >/dev/null
ready=false
for i in $(seq 1 30); do
 if docker exec cuchi-gateway test -f /run/cuchi-gateway-ready >/dev/null 2>&1; then ready=true; break; fi
 sleep 1
done
[ "$ready" = true ] || { echo CUCHI_ERROR_runtime_start_failed; exit 18; }
wait_for_gateway_egress || exit 23
printf 'CUCHI_GATEWAY_OK\n'
