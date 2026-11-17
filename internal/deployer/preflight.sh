set -eu
[ "$(id -u)" = 0 ] || { echo CUCHI_ERROR_root_required; exit 10; }
. /etc/os-release
[ "$ID" = ubuntu ] || { echo CUCHI_ERROR_ubuntu_lts_required; exit 11; }
case "$VERSION_ID" in 24.04|26.04) ;; *) echo CUCHI_ERROR_ubuntu_lts_required; exit 11 ;; esac
[ "$(uname -m)" = x86_64 ] || { echo CUCHI_ERROR_amd64_required; exit 12; }
[ -c /dev/net/tun ] || { echo CUCHI_ERROR_tun_unavailable; exit 13; }
if command -v ufw >/dev/null && ufw status | grep -q '^Status: active'; then echo CUCHI_ERROR_active_ufw; exit 14; fi
command -v flock >/dev/null
printf 'CUCHI_PREFLIGHT_OK\n'
