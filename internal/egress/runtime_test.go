package egress

import (
	"context"
	"fmt"
	"github.com/NetherQuartz/cu-chi/internal/awg"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Real AWG -> kernel WG packets, with an isolated substitute for Cloudflare.
// This proves routing/fail-closed behavior, NOT Cloudflare registration/service.
func TestEgressPacketsSwitchAndFailure(t *testing.T) {
	image := os.Getenv("WARP_TEST_IMAGE")
	if image == "" {
		t.Skip("set WARP_TEST_IMAGE for Linux packet acceptance")
	}
	run := func(args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		b, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("docker %s: %v\n%s", args[0], err, b)
		}
		return string(b)
	}
	prefix := fmt.Sprintf("cuchi-egress-test-%d", time.Now().UnixNano())
	server, client, exit, target := prefix+"-server", prefix+"-client", prefix+"-exit", prefix+"-target"
	run("network", "create", prefix)
	t.Cleanup(func() {
		if t.Failed() {
			for _, c := range []string{server, client, exit} {
				b, _ := exec.Command("docker", "logs", c).CombinedOutput()
				t.Logf("%s: %s", c, b)
			}
		}
		exec.Command("docker", "rm", "-f", server, client, exit, target).Run()
		exec.Command("docker", "network", "rm", prefix).Run()
	})
	dir := t.TempDir()
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	write := func(name, data string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	settings, keys, wgClient, wgExit := awg.NewSettings(), awg.NewKeys(), awg.NewKeys(), awg.NewKeys()
	probeKeys := awg.NewKeys()
	write("awg0.conf", settings.Server([]awg.Peer{{Public: keys.Public, PSK: keys.PSK, Address: "10.66.0.2"}, {Public: probeKeys.Public, PSK: probeKeys.PSK, Address: awg.ProbeAddress}}))
	write("client.conf", settings.Probe(keys))
	write("probe.conf", settings.Probe(probeKeys))
	write("payload", strings.Repeat("x", 131072))
	write("egress-mode", "direct\n")
	write("warp-address", "172.16.0.2/32\n")
	write("exit.conf", fmt.Sprintf("[Interface]\nPrivateKey = %s\nListenPort = 51821\n[Peer]\nPublicKey = %s\nAllowedIPs = 172.16.0.2/32\n", wgExit.Private, wgClient.Public))
	base := []string{"run", "-d", "--network", prefix, "--cap-drop", "ALL", "--cap-add", "NET_ADMIN", "--device", "/dev/net/tun", "--read-only", "--tmpfs", "/run", "--tmpfs", "/tmp", "--sysctl", "net.ipv4.ip_forward=1", "--sysctl", "net.ipv4.conf.all.rp_filter=0", "--sysctl", "net.ipv4.conf.default.rp_filter=0", "--sysctl", "net.ipv6.conf.all.disable_ipv6=1", "-v", dir + ":/config:ro"}
	ip := func(c string) string {
		return strings.TrimSpace(run("inspect", "-f", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", c))
	}
	ready := func(c string) {
		t.Helper()
		for range 80 {
			if exec.Command("docker", "exec", c, "test", "-f", "/run/cuchi-ready").Run() == nil {
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
		t.Fatalf("%s not ready", c)
	}
	run(append(append([]string{}, base...), "--name", server, "--network-alias", "cuchi-awg", image)...)
	ready(server)
	run("run", "-d", "--network", prefix, "--name", target, "--cap-drop", "ALL", "--read-only", "-v", dir+":/config:ro", "--entrypoint", "httpd", image, "-f", "-p", "9090", "-h", "/config")
	targetIP := ip(target)
	exitScript := `ip link add wg0 type wireguard
wg setconf wg0 /config/exit.conf
ip addr add 172.16.0.1/32 dev wg0
ip link set wg0 mtu 1280 up
ip route add 172.16.0.2/32 dev wg0
iptables -t nat -A POSTROUTING -s 172.16.0.2/32 -o eth0 -j MASQUERADE
touch /run/cuchi-ready
sleep 600`
	run(append(append([]string{}, base...), "--name", exit, "--entrypoint", "sh", image, "-ec", exitScript)...)
	ready(exit)
	write("warp.conf", fmt.Sprintf("[Interface]\nPrivateKey = %s\n[Peer]\nPublicKey = %s\nEndpoint = %s:51821\nAllowedIPs = 0.0.0.0/0\nPersistentKeepalive = 25\n", wgClient.Private, wgExit.Public, ip(exit)))
	clientScript := `amneziawg-go -f probe0 >/tmp/probe.log 2>&1 &
for i in $(seq 1 50); do [ -S /run/amneziawg/probe0.sock ] && break; sleep 0.1; done
awg setconf probe0 /config/client.conf
ip addr add 10.66.0.2/32 dev probe0
ip link set probe0 mtu 1280 up
ip route add "$1/32" dev probe0
ip route add 1.1.1.1/32 dev probe0
touch /run/cuchi-ready
sleep 600`
	run(append(append([]string{}, base...), "--name", client, "--entrypoint", "sh", image, "-ec", clientScript, "--", targetIP)...)
	ready(client)
	url := "http://" + targetIP + ":9090/payload"
	transfer := func() {
		t.Helper()
		data := run("exec", client, "curl", "--fail", "--silent", "--show-error", "--retry", "2", "--max-time", "8", url)
		if data != strings.Repeat("x", 131072) {
			t.Fatal("large transfer incomplete")
		}
	}
	deny := func() {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if exec.CommandContext(ctx, "docker", "exec", client, "curl", "--fail", "--silent", "--max-time", "3", url).Run() == nil {
			t.Fatal("failed WARP leaked direct traffic")
		}
		run("exec", server, "curl", "--fail", "--silent", "--max-time", "3", "--output", "/tmp/direct-proof", url)
	}
	switchTo := func(mode string) {
		t.Helper()
		write("egress-mode", mode+"\n")
		run("exec", server, "/usr/local/bin/cuchi-egress", mode)
	}
	transfer()
	before := run("exec", server, "awg", "show", "awg0", "public-key")
	switchTo("warp")
	transfer()
	// Prove packets actually crossed kernel WG; a successful direct curl is not enough.
	counters := strings.Fields(run("exec", exit, "wg", "show", "wg0", "transfer"))
	if len(counters) != 3 || counters[1] == "0" || counters[2] == "0" {
		t.Fatal("no WARP tunnel transfer")
	}
	if !strings.Contains(run("exec", server, "ip", "route", "get", "1.1.1.1"), "dev eth0") {
		t.Fatal("locally originated management traffic rerouted")
	}
	if os.Getenv("AWG_TEST_EGRESS") == "true" {
		dns := run("exec", client, "dig", "@1.1.1.1", "example.com", "A", "+short", "+time=5", "+tries=2")
		if strings.TrimSpace(dns) == "" {
			t.Fatal("DNS failed through WARP substitute")
		}
		trace := run("exec", client, "curl", "--fail", "--silent", "--max-time", "20", "https://1.1.1.1/cdn-cgi/trace")
		if !strings.Contains(trace, "warp=off") {
			t.Fatal("unexpected real WARP on isolated CI exit")
		}
		run("run", "--rm", "--network", prefix, "--cap-drop", "ALL", "--cap-add", "NET_ADMIN", "--device", "/dev/net/tun", "--tmpfs", "/run", "--tmpfs", "/tmp", "-v", dir+":/config:ro", "--entrypoint", "/usr/local/bin/cuchi-probe", image)
		// The production health probe must reject a generic WG exit without WARP.
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		err := exec.CommandContext(ctx, "docker", "run", "--rm", "--network", prefix, "--cap-drop", "ALL", "--cap-add", "NET_ADMIN", "--device", "/dev/net/tun", "--tmpfs", "/run", "--tmpfs", "/tmp", "-v", dir+":/config:ro", "-e", "CUCHI_EXPECT_WARP=warp", "--entrypoint", "/usr/local/bin/cuchi-probe", image).Run()
		cancel()
		if err == nil {
			t.Fatal("production WARP verification accepted warp=off")
		}
	}
	// Removing the exit peer must not trigger a direct fallback.
	run("exec", exit, "wg", "set", "wg0", "peer", wgClient.Public, "remove")
	deny()
	run("exec", exit, "wg", "setconf", "wg0", "/config/exit.conf")
	transfer()
	run("exec", server, "ip", "link", "del", "warp0")
	deny()
	switchTo("direct")
	transfer()
	switchTo("warp")
	transfer()
	run("restart", server)
	ready(server)
	transfer()
	if before != run("exec", server, "awg", "show", "awg0", "public-key") {
		t.Fatal("AWG identity changed")
	}
	switchTo("direct")
	transfer()
	t.Log("Direct/WARP/off, DNS/TLS, large TCP, peer and interface failure, restart persistence; same client profile")
}
