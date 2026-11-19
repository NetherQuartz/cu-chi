package deployer

import (
	"archive/tar"
	"bytes"
	"github.com/NetherQuartz/cu-chi/internal/cryptobox"
	"github.com/NetherQuartz/cu-chi/internal/xray"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Run the actual production installer on the disposable Ubuntu GitHub runner.
func TestXrayDeployment(t *testing.T) {
	if os.Getenv("XRAY_DEPLOY_TEST") != "true" {
		t.Skip("requires disposable Linux Docker runner")
	}
	id := cryptobox.ID()
	run := func(script string, args ...string) *exec.Cmd {
		return exec.Command("sudo", append([]string{"bash", "-c", script, "--"}, args...)...)
	}
	t.Cleanup(func() {
		run(`for c in cuchi-xray cuchi-xray-probe cuchi-xray-previous; do if [ "$(docker inspect -f '{{index .Config.Labels "io.cuchi.xray"}}' "$c" 2>/dev/null)" = "$1" ]; then docker rm -f "$c"; fi; done; rm -rf "/opt/cu-chi/$1/xray"`, id).Run()
	})
	out, e := run(targetProbe).Output()
	if e != nil || len(strings.Fields(string(out))) == 0 {
		t.Fatal("TLS target selection failed", e)
	}
	k := xray.Generate()
	for _, domain := range strings.Fields(string(out)) {
		uuid := cryptobox.ID()
		server, client, e := xray.Configs(k, domain, uuid, nil)
		if e != nil {
			t.Fatal(e)
		}
		var b bytes.Buffer
		tw := tar.NewWriter(&b)
		for name, data := range map[string][]byte{"server.json": server, "client.json": client} {
			tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(data))})
			tw.Write(data)
		}
		tw.Close()
		cmd := run(xrayApply, id, "1", xray.Image)
		cmd.Stdin = &b
		out, e := cmd.CombinedOutput()
		if e == nil && strings.Contains(string(out), "CUCHI_XRAY_OK") {
			return
		}
		t.Logf("apply on %s: %s", domain, out)
	}
	t.Fatal("REALITY data-plane probe failed for available targets")
}
