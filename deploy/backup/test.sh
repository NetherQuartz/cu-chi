#!/bin/sh
# Destructive only to the uniquely named test database and volume created here.
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
volume="cuchi-backup-test-$(date +%s)-$$"
restore="cuchi_restore_$$"
probe="backup_probe_$$"
cleanup() {
 docker run --rm --network host -e PGHOST="${BACKUP_TEST_HOST:-127.0.0.1}" -e PGPORT="${BACKUP_TEST_PORT:-5432}" -e PGUSER=cuchi -e PGPASSWORD="${BACKUP_TEST_PASSWORD:-ci-only-password}" postgres:17-alpine dropdb --if-exists "$restore" >/dev/null 2>&1 || true
 docker run --rm --network host -e PGHOST="${BACKUP_TEST_HOST:-127.0.0.1}" -e PGPORT="${BACKUP_TEST_PORT:-5432}" -e PGUSER=cuchi -e PGDATABASE="${BACKUP_TEST_DATABASE:-cuchi_test}" -e PGPASSWORD="${BACKUP_TEST_PASSWORD:-ci-only-password}" postgres:17-alpine psql -q -c "DROP TABLE IF EXISTS $probe" >/dev/null 2>&1 || true
 docker volume rm "$volume" >/dev/null 2>&1 || true
}
trap cleanup EXIT
docker volume create "$volume" >/dev/null
# Model a bind directory created by a NAS user rather than by Docker root.
docker run --rm -v "$volume:/backups" --entrypoint /bin/sh postgres:17-alpine -c 'chown 1026:100 /backups; chmod 700 /backups'
run() {
 docker run --rm --network host --read-only --cap-drop ALL --cap-add CHOWN --security-opt no-new-privileges --tmpfs /tmp \
  -e PGHOST="${BACKUP_TEST_HOST:-127.0.0.1}" -e PGPORT="${BACKUP_TEST_PORT:-5432}" -e PGUSER=cuchi -e PGDATABASE="${BACKUP_TEST_DATABASE:-cuchi_test}" -e PGPASSWORD="${BACKUP_TEST_PASSWORD:-ci-only-password}" \
  -e CUCHI_MASTER_KEY=fixture-only -e CUCHI_IMAGE_TAG=test -e RESTORE_DB="$restore" -e PROBE_TABLE="$probe" -v "$volume:/backups" -v "$root/deploy/backup/backup.sh:/usr/local/bin/cuchi-backup:ro" \
  --entrypoint /bin/sh postgres:17-alpine "$@"
}
run -ec 'psql -q -v ON_ERROR_STOP=1 -c "CREATE TABLE $PROBE_TABLE(value text); INSERT INTO $PROBE_TABLE VALUES (\$\$restore-fixture\$\$)"'
run /usr/local/bin/cuchi-backup --once
run /usr/local/bin/cuchi-backup --healthcheck
run -ec '
 bundle=$(find /backups -maxdepth 1 -name "cuchi-*" -type d)
 test "$(stat -c %a "$bundle/database.dump")" = 600
 test "$(stat -c %a "$bundle/master-key")" = 600
 test "$(cat "$bundle/master-key")" = fixture-only
 test "$(cat "$bundle/image-tag")" = test
 createdb "$RESTORE_DB"
 pg_restore --exit-on-error --dbname="$RESTORE_DB" "$bundle/database.dump"
 test "$(psql -At -d "$RESTORE_DB" -c "SELECT value FROM $PROBE_TABLE")" = restore-fixture
 # Old successful snapshots are retained until a NEW backup succeeds.
 for n in $(seq -w 1 15); do mkdir "/backups/cuchi-20200101T0000${n}Z-testxx"; touch "/backups/cuchi-20200101T0000${n}Z-testxx/complete"; done
 count=$(find /backups -maxdepth 1 -name "cuchi-*" -type d | wc -l)
 if PGDATABASE=cuchi_nonexistent_fixture sh /usr/local/bin/cuchi-backup --once; then exit 1; fi
 test "$(find /backups -maxdepth 1 -name "cuchi-*" -type d | wc -l)" = "$count"
 if sh /usr/local/bin/cuchi-backup --healthcheck; then exit 1; fi
 sh /usr/local/bin/cuchi-backup --once
 sh /usr/local/bin/cuchi-backup --healthcheck
 test "$(find /backups -maxdepth 1 -name "cuchi-*" -type d | wc -l)" = 14
 test "$(find /backups -maxdepth 1 -name ".pending.*" -type d | wc -l)" = 0
 printf "Backup restore, permissions, failure handling and retention passed\n"
'
