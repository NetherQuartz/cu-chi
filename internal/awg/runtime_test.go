package awg

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Uses independent network namespaces and real AWG packets. Egress is another
// container reached through AWG + NAT; CI additionally verifies public DNS/TLS.
func TestRuntimeRoundTrip(t *testing.T) {
	image := os.Getenv("AWG_TEST_IMAGE")
	if image == "" {
		t.Skip("set AWG_TEST_IMAGE for real TUN integration")
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
	name := fmt.Sprintf("cuchi-test-%d", time.Now().UnixNano())
	server, client, egress := name+"-server", name+"-client", name+"-egress"
	run("network", "create", name)
	t.Cleanup(func() {
		if t.Failed() {
			for _, c := range []string{server, client} {
				for _, args := range [][]string{{"logs", c}, {"exec", c, "cat", "/tmp/awg.log"}, {"exec", c, "cat", "/tmp/probe.log"}} {
					b, _ := exec.Command("docker", args...).CombinedOutput()
					t.Logf("%s: %s", c, b)
				}
			}
		}
		exec.Command("docker", "rm", "-f", server, client, egress).Run()
		exec.Command("docker", "network", "rm", name).Run()
	})
	dir := t.TempDir()
	settings, keys := NewSettings(), NewKeys()
	write := func(name, data string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("awg0.conf", settings.Server([]Peer{{Public: keys.Public, PSK: keys.PSK, Address: ProbeAddress}}))
	write("probe.conf", settings.Probe(keys))
	write("payload", strings.Repeat("x", 65536))
	run("run", "-d", "--name", server, "--network", name, "--network-alias", "cuchi-awg", "--cap-drop", "ALL", "--cap-add", "NET_ADMIN", "--device", "/dev/net/tun", "--read-only", "--tmpfs", "/run", "--tmpfs", "/tmp", "--sysctl", "net.ipv4.ip_forward=1", "-v", dir+":/config:ro", image)
	ready := func(c, path string) {
		t.Helper()
		for range 50 {
			if exec.Command("docker", "exec", c, "test", "-f", path).Run() == nil {
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
		t.Fatalf("%s not ready", c)
	}
	ready(server, "/run/cuchi-ready")
	if !strings.Contains(run("exec", server, "amneziawg-go", "--version"), Version) || !strings.Contains(run("exec", server, "awg", "--version"), ToolsVersion) {
		t.Fatal("incorrect runtime version")
	}
	run("exec", server, "httpd", "-p", "10.66.0.1:9090", "-h", "/config")
	run("run", "-d", "--name", egress, "--network", name, "--cap-drop", "ALL", "--read-only", "-v", dir+":/config:ro", "--entrypoint", "httpd", image, "-f", "-p", "9090", "-h", "/config")
	egressIP := strings.TrimSpace(run("inspect", "-f", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", egress))
	if net.ParseIP(egressIP) == nil {
		t.Fatal("invalid test egress IP")
	}
	script := `amneziawg-go -f probe0 >/tmp/probe.log 2>&1 &
for i in $(seq 1 50); do [ -S /run/amneziawg/probe0.sock ] && break; sleep 0.1; done
awg setconf probe0 /config/probe.conf
ip addr add 10.66.255.254/32 dev probe0
ip link set probe0 mtu 1280 up
ip route add 10.66.0.1/32 dev probe0
ip route add 1.1.1.1/32 dev probe0
ip route add "$1/32" dev probe0
touch /run/cuchi-ready
sleep 300`
	run("run", "-d", "--name", client, "--network", name, "--cap-drop", "ALL", "--cap-add", "NET_ADMIN", "--device", "/dev/net/tun", "--read-only", "--tmpfs", "/run", "--tmpfs", "/tmp", "-v", dir+":/config:ro", "--entrypoint", "sh", image, "-ec", script, "--", egressIP)
	ready(client, "/run/cuchi-ready")
	transfer := func(host string) {
		t.Helper()
		data := run("exec", client, "curl", "--fail", "--silent", "--show-error", "--retry", "2", "--max-time", "10", "http://"+host+":9090/payload")
		if len(data) != 65536 {
			t.Fatalf("MTU/traffic test got %d bytes", len(data))
		}
	}
	transfer("10.66.0.1")
	transfer(egressIP)
	if os.Getenv("AWG_TEST_EGRESS") == "true" {
		dns := run("exec", client, "dig", "@1.1.1.1", "example.com", "A", "+short", "+time=5", "+tries=2")
		valid := false
		for _, line := range strings.Fields(dns) {
			if net.ParseIP(line) != nil {
				valid = true
			}
		}
		if !valid {
			t.Fatal("tunnel DNS returned no IP")
		}
		run("exec", client, "curl", "--fail", "--silent", "--show-error", "--connect-timeout", "5", "--max-time", "20", "https://1.1.1.1/cdn-cgi/trace")
	}
	run("exec", server, "awg", "syncconf", "awg0", "/config/awg0.conf")
	transfer(egressIP)
	write("awg0.conf", settings.Server(nil))
	run("exec", server, "awg", "syncconf", "awg0", "/config/awg0.conf")
	if strings.TrimSpace(run("exec", server, "awg", "show", "awg0", "peers")) != "" {
		t.Fatal("peer not revoked")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if exec.CommandContext(ctx, "docker", "exec", client, "curl", "--fail", "--silent", "--max-time", "3", "http://"+egressIP+":9090/payload").Run() == nil {
		t.Fatal("revoked peer still transfers data")
	}
}
