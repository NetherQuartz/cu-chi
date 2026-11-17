set -eu
umask 077
server_id="$1"; generation="$2"; peer="$3"
exec 9>/run/lock/cuchi-awg.lock
flock -w 2 9 || { echo HEALTH_BUSY; exit 0; }
base="/opt/cu-chi/$server_id"
[ -f "$base/generation" ] && [ "$(cat "$base/generation")" = "$generation" ] || { echo HEALTH_BUSY; exit 0; }
# An absent/stopped owned runtime is a confirmed service failure. Never mutate a foreign one.
if ! docker container inspect cuchi-awg >/dev/null 2>&1; then echo HEALTH_DATA; exit 0; fi
[ "$(docker inspect -f '{{ index .Config.Labels "io.cuchi.server" }}' cuchi-awg)" = "$server_id" ] || exit 1
[ "$(docker inspect -f '{{.State.Running}}' cuchi-awg)" = true ] || { echo HEALTH_DATA; exit 0; }
image=$(docker inspect -f '{{.Config.Image}}' cuchi-awg)
[[ "$image" =~ ^ghcr\.io/netherquartz/cu-chi-awg@sha256:[a-f0-9]{64}$ ]] || exit 1
# The journal contains only the public synthetic key. Recover after abrupt process death.
name="cuchi-health-$server_id"
if docker container inspect "$name" >/dev/null 2>&1; then
 [ "$(docker inspect -f '{{ index .Config.Labels "io.cuchi.health" }}' "$name")" = "$server_id" ] || exit 1
 docker rm -f "$name" >/dev/null
fi
if [ -f "$base/health-peer" ]; then
 old=$(cat "$base/health-peer")
 [[ "$old" =~ ^[A-Za-z0-9+/]{43}=$ ]] || exit 1
 docker exec cuchi-awg awg set awg0 peer "$old" remove >/dev/null || exit 1
 rm -f "$base/health-peer"
fi
# Fixed, root-owned directory; no private probe material persists between attempts.
install -d -m 700 "$base/health"
rm -f "$base/health/probe.conf" "$base/health/psk"
cleanup() {
 cleaned=true
 docker rm -f "$name" >/dev/null 2>&1 || true
 if docker exec cuchi-awg awg set awg0 peer "$peer" remove >/dev/null 2>&1; then rm -f "$base/health-peer"; else cleaned=false; fi
 docker exec cuchi-awg rm -f /tmp/cuchi-health-psk >/dev/null 2>&1 || true
 rm -f "$base/health/probe.conf" "$base/health/psk"
 [ "$cleaned" = true ]
}
trap cleanup EXIT
trap 'exit 1' INT TERM HUP
# Receive only the two fixed archive entries produced by the control plane.
tar --no-same-owner -xf - -C "$base/health"
printf '%s\n' "$peer" > "$base/health-peer"
docker exec -i cuchi-awg sh -c 'umask 077; cat > /tmp/cuchi-health-psk' < "$base/health/psk"
if ! docker exec cuchi-awg awg set awg0 peer "$peer" preshared-key /tmp/cuchi-health-psk allowed-ips 10.66.255.254/32 >/dev/null; then echo HEALTH_DATA; exit 0; fi
# timeout bounds the remote operation even if the NAS disconnects.
if timeout --signal=TERM --kill-after=5s 70s docker run --rm --pull=never --name "$name" --label "io.cuchi.health=$server_id" --network cuchi-awg \
 --cap-drop ALL --cap-add NET_ADMIN --device /dev/net/tun --security-opt no-new-privileges \
 --read-only --tmpfs /run --tmpfs /tmp -v "$base/health:/config:ro" --entrypoint /usr/local/bin/cuchi-probe "$image" >/dev/null 2>&1; then
 outcome=HEALTH_OK
else
 outcome=HEALTH_DATA
fi
# A successful probe is acknowledged only after its peer has been removed.
if cleanup; then trap - EXIT; echo "$outcome"; else trap - EXIT; exit 1; fi
