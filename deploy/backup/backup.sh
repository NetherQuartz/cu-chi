#!/bin/sh
set -eu
case "${1:-}" in
 --healthcheck)
  [ ! -e /backups/.failed ] || exit 1
  [ -f /backups/.last-success ] || exit 1
  stamp=$(cat /backups/.last-success)
  case "$stamp" in ''|*[!0-9]*) exit 1;; esac
  age=$(( $(date +%s) - stamp ))
  [ "$age" -ge 0 ] && [ "$age" -lt 93600 ]
  exit $?
 ;;
 --once)
  umask 077
  mkdir -p /backups
  chmod 700 /backups
  exec 9>/backups/.lock
  flock 9
  stage=$(mktemp -d /backups/.pending.XXXXXX)
  trap 'rm -rf -- "$stage"; date +%s > /backups/.failed' EXIT
  pg_dump --no-password --format=custom --file="$stage/database.dump"
  pg_restore --list "$stage/database.dump" >/dev/null
  # A database backup cannot decrypt credentials without its original master key.
  printf '%s\n' "${CUCHI_MASTER_KEY:?Master key required for recoverable backups}" > "$stage/master-key"
  printf '%s\n' "${CUCHI_IMAGE_TAG:-main}" > "$stage/image-tag"
  chmod 600 "$stage/database.dump" "$stage/master-key" "$stage/image-tag"
  printf '%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "$stage/complete"
  destination="/backups/cuchi-$(date -u +%Y%m%dT%H%M%SZ)-${stage##*.}"
  mv "$stage" "$destination"
  date +%s > /backups/.last-success.new
  mv /backups/.last-success.new /backups/.last-success
  rm -f /backups/.failed
  trap - EXIT
  # Only completed bundles created by this script participate in retention.
  for path in /backups/cuchi-????????T??????Z-??????; do
   [ -d "$path" ] && [ ! -L "$path" ] && [ -f "$path/complete" ] && printf '%s\n' "$path"
  done | sort -r | tail -n +15 | while IFS= read -r path; do rm -rf -- "$path"; done
  printf 'Cuchi backup completed\n'
 ;;
 '')
  while :; do
   if /bin/sh "$0" --once; then sleep 86400; else printf 'Cuchi backup failed; retry in one hour\n' >&2; sleep 3600; fi
  done
 ;;
 *) exit 2 ;;
esac
