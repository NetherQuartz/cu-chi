set -eu
umask 077
server_id="$1"; image="$2"
exec 9>/run/lock/cuchi-awg.lock
flock -w 120 9
base="/opt/cu-chi/$server_id/warp-registration"
install -d -m 700 "$base"
helper="cuchi-warp-register-$server_id"
cleanup() {
 if [ "$(docker inspect -f '{{ index .Config.Labels "io.cuchi.server" }}' "$helper" 2>/dev/null)" = "$server_id" ]; then
  docker rm -f "$helper" >/dev/null 2>&1 || true
 fi
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
if docker container inspect "$helper" >/dev/null 2>&1; then
 [ "$(docker inspect -f '{{ index .Config.Labels "io.cuchi.server" }}' "$helper")" = "$server_id" ] || { echo CUCHI_ERROR_foreign_container; exit 15; }
 cleanup
fi
# Registration is cached on the VPS before returning its profile, so interrupted
# NAS writes do not create a fresh Cloudflare identity on each retry.
if [ ! -s "$base/wgcf-profile.conf" ]; then
 docker pull "$image" >/dev/null
 if ! timeout --signal=TERM --kill-after=5s 120s docker run --rm --name "$helper" --label "io.cuchi.server=$server_id" --network bridge --cap-drop ALL --security-opt no-new-privileges \
  --read-only --tmpfs /tmp -v "$base:/work" -w /work --entrypoint /bin/sh "$image" -ec '
   umask 077
   if [ ! -s wgcf-account.toml ]; then wgcf register --accept-tos >/dev/null 2>&1; fi
   wgcf generate --keepalive -p pending.conf >/dev/null 2>&1
   chmod 600 pending.conf
   mv pending.conf wgcf-profile.conf
  ' >/dev/null 2>&1; then echo CUCHI_ERROR_warp_registration_failed; exit 24; fi
fi
cat "$base/wgcf-profile.conf"
