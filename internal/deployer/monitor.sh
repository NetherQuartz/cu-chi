set -euo pipefail
export LC_ALL=C
exec 9>/run/lock/cuchi-awg.lock
flock -w 2 9
printf 'BOOT %s\n' "$(cat /proc/sys/kernel/random/boot_id)"
awk '/^cpu / {total=0;for(i=2;i<=9;i++)total+=$i;printf "CPU %.0f %.0f\n",total,$5+$6} /^cpu[0-9]+ /{n++} END{print "CORES " n}' /proc/stat
awk '/^MemTotal:/{t=$2} /^MemAvailable:/{a=$2} END{printf "MEM %.0f %.0f\n",t*1024,(t-a)*1024}' /proc/meminfo
df -B1 --output=size,used / | awk 'NR==2{print "DISK " $1 " " $2}'
awk '{print "UPTIME " $1}' /proc/uptime
iface=$(ip -o -4 route show default | awk 'NR==1{for(i=1;i<=NF;i++)if($i=="dev"){print $(i+1);exit}}')
[[ "$iface" =~ ^[a-zA-Z0-9_.:-]{1,64}$ ]]
printf 'NET %s %s %s\n' "$iface" "$(cat "/sys/class/net/$iface/statistics/rx_bytes")" "$(cat "/sys/class/net/$iface/statistics/tx_bytes")"
if [ "$(docker inspect -f '{{ index .Config.Labels "io.cuchi.server" }}' cuchi-awg 2>/dev/null)" = "$1" ] && [ "$(docker inspect -f '{{.State.Running}}' cuchi-awg)" = true ]; then
 epoch=$(docker inspect -f '{{.State.StartedAt}}' cuchi-awg)
 # These selectors never output private or preshared keys, unlike `show dump`.
 if transfers=$(docker exec cuchi-awg awg show awg0 transfer) && handshakes=$(docker exec cuchi-awg awg show awg0 latest-handshakes); then
  printf 'TUNNEL 1\nEPOCH %s\n' "$epoch"
  [ -z "$transfers" ] || printf '%s\n' "$transfers" | awk '{print "PEER " $1 " " $2 " " $3}'
  [ -z "$handshakes" ] || printf '%s\n' "$handshakes" | awk '{print "HANDSHAKE " $1 " " $2}'
 else printf 'TUNNEL 0\n'; fi
else printf 'TUNNEL 0\n'; fi
printf 'END\n'
