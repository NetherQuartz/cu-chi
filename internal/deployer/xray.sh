set -eu
umask 077
id=$1
revision=$2
image=$3
base="/opt/cu-chi/$id/xray"
name=cuchi-xray
previous=cuchi-xray-previous
probe=cuchi-xray-probe
exec 9>/run/lock/cuchi-xray.lock
flock -w 90 9
. /etc/os-release
[ "$ID" = ubuntu ] && { [ "$VERSION_ID" = 24.04 ] || [ "$VERSION_ID" = 26.04 ]; } || exit 1
if ! command -v docker >/dev/null || ! command -v curl >/dev/null; then
 export DEBIAN_FRONTEND=noninteractive
 apt-get -o DPkg::Lock::Timeout=120 update >/dev/null
 apt-get -o DPkg::Lock::Timeout=120 install -y docker.io curl ca-certificates >/dev/null
fi
systemctl enable --now docker >/dev/null
mkdir -p "$base"
if [ -f "$base/generation" ] && [ "$(cat "$base/generation")" -gt "$revision" ]; then exit 1; fi
exists() { docker container inspect "$1" >/dev/null 2>&1; }
for c in "$name" "$previous" "$probe"; do
 if exists "$c"; then
  [ "$(docker inspect -f '{{index .Config.Labels "io.cuchi.xray"}}' "$c")" = "$id" ] || { echo CUCHI_ERROR_foreign_container; exit 1; }
 fi
done
if exists "$probe"; then docker rm -f "$probe" >/dev/null; fi
# Recover a stopped previous runtime if an earlier apply was interrupted.
if exists "$previous"; then
 if exists "$name"; then docker rm -f "$name" >/dev/null; fi
 docker rename "$previous" "$name"
 docker start "$name" >/dev/null
fi
stage=$(mktemp -d "$base/release.XXXXXXXX")
swapped=false
done_ok=false
cleanup() {
 set +e
 docker rm -f "$probe" >/dev/null 2>&1
 rm -f "$stage/client.json"
 if [ "$done_ok" != true ]; then
  if [ "$swapped" = true ]; then
   docker rm -f "$name" >/dev/null 2>&1
   if exists "$previous"; then docker rename "$previous" "$name" && docker start "$name" >/dev/null; fi
  fi
  rm -rf "$stage"
 fi
}
trap cleanup EXIT
trap 'exit 1' HUP TERM INT
tar --no-same-owner -xf - -C "$stage" server.json client.json
# Mounted files are readable only by the runtime UID and root.
chown 65532 "$stage/server.json" "$stage/client.json"
safe=(--user 65532 --cap-drop ALL --security-opt no-new-privileges --read-only --label "io.cuchi.xray=$id")
docker image inspect "$image" >/dev/null 2>&1 || docker pull "$image" >/dev/null
docker run --rm "${safe[@]}" -v "$stage/server.json:/config.json:ro" "$image" run -test -c /config.json >/dev/null
if exists "$name"; then docker rename "$name" "$previous"; fi
swapped=true
if exists "$previous"; then docker stop "$previous" >/dev/null; fi
docker run -d "${safe[@]}" --name "$name" --restart unless-stopped -p 443:8443/tcp -v "$stage/server.json:/config.json:ro" "$image" run -c /config.json >/dev/null
docker run -d "${safe[@]}" --name "$probe" --network "container:$name" -v "$stage/client.json:/config.json:ro" "$image" run -c /config.json >/dev/null
pid=$(docker inspect -f '{{.State.Pid}}' "$name")
verified=false
for i in 1 2 3; do
 if nsenter -n -t "$pid" curl --noproxy '' --socks5-hostname 127.0.0.1:1080 --fail --silent --max-time 10 https://example.org/ >/dev/null; then verified=true; break; fi
 sleep 1
done
[ "$verified" = true ] || { echo CUCHI_ERROR_xray_probe_failed; exit 1; }
printf '%s\n' "$revision" > "$base/generation.new"
mv "$base/generation.new" "$base/generation"
done_ok=true
if exists "$previous"; then docker rm "$previous" >/dev/null; fi
for d in "$base"/release.*; do [ "$d" = "$stage" ] || rm -rf "$d"; done
echo CUCHI_XRAY_OK
