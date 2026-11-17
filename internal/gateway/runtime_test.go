package gateway

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NetherQuartz/cu-chi/internal/awg"
)

// Linux-only packet acceptance: ordinary kernel WG -> userspace AWG -> egress.
// The egress host is reachable directly too, so a fail-open routing bug would
// make the negative tests succeed and fail this test.
func TestGatewayPacketsAndFailClosed(t *testing.T) {
	image, serverImage := os.Getenv("GATEWAY_TEST_IMAGE"), os.Getenv("AWG_TEST_IMAGE")
	if image == "" || serverImage == "" {
		t.Skip("set GATEWAY_TEST_IMAGE and AWG_TEST_IMAGE for real Linux packet acceptance")
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
	prefix := fmt.Sprintf("cuchi-gateway-test-%d", time.Now().UnixNano())
	server, client, bridge, egress := prefix+"-server", prefix+"-client", prefix+"-bridge", prefix+"-egress"
	run("network", "create", prefix)
	t.Cleanup(func() {
		if t.Failed() {
			for _, c := range []string{bridge, server} {
				b, _ := exec.Command("docker", "logs", c).CombinedOutput()
				t.Logf("%s logs: %s", c, b)
			}
		}
		exec.Command("docker", "rm", "-f", bridge, server, client, egress).Run()
		exec.Command("docker", "network", "rm", prefix).Run()
	})
	dir := t.TempDir()
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	write := func(name, value string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0644); err != nil {
			t.Fatal(err)
		}
	}
	settings, keys := awg.NewSettings(), awg.NewKeys()
	write("awg0.conf", settings.Server([]awg.Peer{{Public: keys.Public, PSK: keys.PSK, Address: "10.66.0.2"}}))
	write("payload", strings.Repeat("x", 131072))
	base := []string{"run", "-d", "--network", prefix, "--cap-drop", "ALL", "--cap-add", "NET_ADMIN", "--device", "/dev/net/tun", "--read-only", "--tmpfs", "/run", "--tmpfs", "/tmp"}
	run(append(append([]string{}, base...), "--name", server, "--sysctl", "net.ipv4.ip_forward=1", "-v", dir+":/config:ro", serverImage)...)
	ip := func(c string) string {
		return strings.TrimSpace(run("inspect", "-f", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", c))
	}
	waitReady := func(c, path string) {
		t.Helper()
		for range 80 {
			if exec.Command("docker", "exec", c, "test", "-f", path).Run() == nil {
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
		t.Fatalf("%s did not become ready", c)
	}
	waitReady(server, "/run/cuchi-ready")
	run(append(append([]string{}, base...), "--name", client, "-v", dir+":/config:ro", "--entrypoint", "sleep", image, "600")...)
	run("run", "-d", "--network", prefix, "--name", egress, "--cap-drop", "ALL", "--read-only", "-v", dir+":/config:ro", "--entrypoint", "httpd", serverImage, "-f", "-p", "9090", "-h", "/config")
	target := ip(egress)
	lan, err := NewLAN("192.168.50.10", ip(client), "192.168.55.0/24")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(dir, "lan"), 0755); err != nil {
		t.Fatal(err)
	}
	write("lan/wg0.conf", lan.ServerConfig)
	write("lan/router-ip", ip(client))
	write("uplink.conf", settings.Client(keys, "10.66.0.2", ip(server), 51820))
	run(append(append([]string{}, base...), "--name", bridge, "--sysctl", "net.ipv4.ip_forward=1", "--sysctl", "net.ipv4.conf.all.rp_filter=0", "--sysctl", "net.ipv4.conf.default.rp_filter=0", "--sysctl", "net.ipv6.conf.all.disable_ipv6=1", "-v", dir+":/config:ro", image)...)
	waitReady(bridge, "/run/cuchi-gateway-ready")
	routerConfig := strings.Replace(lan.RouterConfig, "192.168.50.10:51820", ip(bridge)+":51820", 1)
	lines := []string{}
	for _, line := range strings.Split(routerConfig, "\n") {
		if strings.HasPrefix(line, "Address =") || strings.HasPrefix(line, "DNS =") || strings.HasPrefix(line, "MTU =") {
			continue
		}
		lines = append(lines, line)
	}
	write("client.conf", strings.Join(lines, "\n"))

	run("exec", client, "sh", "-ec", `ip link add wg0 type wireguard
wg setconf wg0 /config/client.conf
ip addr add 10.77.0.2/32 dev wg0
ip addr add 192.168.55.9/32 dev lo
ip link set wg0 mtu 1280 up
ip route add "$1/32" dev wg0
ip route add 1.1.1.1/32 dev wg0`, "--", target)
	transfer := func(source string) {
		t.Helper()
		data := run("exec", client, "curl", "--interface", source, "--fail", "--silent", "--show-error", "--retry", "2", "--max-time", "8", "http://"+target+":9090/payload")
		if len(data) != 131072 {
			t.Fatalf("large transfer: %d bytes", len(data))
		}
	}
	transfer("10.77.0.2")
	transfer("192.168.55.9")
	if !strings.Contains(run("exec", bridge, "ip", "route", "get", "1.1.1.1"), "dev eth0") {
		t.Fatal("management routing changed")
	}
	if !strings.Contains(run("exec", bridge, "ip6tables", "-S", "FORWARD"), "-P FORWARD DROP") {
		t.Fatal("IPv6 forwarding not blocked")
	}
	if os.Getenv("AWG_TEST_EGRESS") == "true" {
		dns := run("exec", client, "dig", "-b", "10.77.0.2", "@1.1.1.1", "example.com", "A", "+short", "+time=5", "+tries=2")
		if strings.TrimSpace(dns) == "" {
			t.Fatal("DNS through both tunnels failed")
		}
		run("exec", client, "curl", "--interface", "10.77.0.2", "--fail", "--silent", "--show-error", "--max-time", "20", "https://1.1.1.1/cdn-cgi/trace")
		run("exec", bridge, "/usr/local/bin/cuchi-gateway-healthcheck")
	}
	deny := func() {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if exec.CommandContext(ctx, "docker", "exec", client, "curl", "--interface", "10.77.0.2", "--fail", "--silent", "--max-time", "3", "http://"+target+":9090/payload").Run() == nil {
			t.Fatal("client traffic bypassed failed AWG")
		}
	}
	run("exec", bridge, "awg", "set", "awg0", "peer", settings.Keys.Public, "remove")
	deny()
	run("exec", bridge, "pkill", "-STOP", "-x", "amneziawg-go")
	run("exec", bridge, "ip", "link", "del", "awg0")
	deny()
	// The namespace may now stop with the AWG process; restarting restores it.
	run("restart", bridge)
	waitReady(bridge, "/run/cuchi-gateway-ready")
	transfer("10.77.0.2")
	publicBefore := run("exec", bridge, "wg", "show", "wg0", "public-key")
	// Rotate uplink keys/address, keeping the LAN identity and router configuration.
	next := awg.NewKeys()
	write("awg0.conf", settings.Server([]awg.Peer{{Public: next.Public, PSK: next.PSK, Address: "10.66.0.3"}}))
	run("exec", server, "awg", "syncconf", "awg0", "/config/awg0.conf")
	write("uplink.conf", settings.Client(next, "10.66.0.3", ip(server), 51820))
	run("restart", bridge)
	waitReady(bridge, "/run/cuchi-gateway-ready")
	if publicBefore != run("exec", bridge, "wg", "show", "wg0", "public-key") {
		t.Fatal("LAN identity changed on uplink rotation")
	}
	transfer("10.77.0.2")
	transfer("192.168.55.9")
}
