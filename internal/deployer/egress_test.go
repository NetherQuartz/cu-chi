package deployer

import (
	"archive/tar"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyEgressRollback(t *testing.T) {
	for _, scenario := range []string{"success", "failed_enable", "failed_disable", "rollback_failed", "module_missing"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			id := "11111111-1111-1111-1111-111111111111"
			base := filepath.Join(dir, id)
			os.MkdirAll(filepath.Join(base, "config"), 0700)
			write := func(name, data string) {
				t.Helper()
				if err := os.WriteFile(name, []byte(data), 0700); err != nil {
					t.Fatal(err)
				}
			}
			old, desired := "direct", "warp"
			if scenario == "failed_disable" {
				old, desired = "warp", "direct"
			}
			write(filepath.Join(base, "config/egress-mode"), old)
			docker := `#!/bin/bash
printf '%s\n' "$*" >> "$TEST_LOG"
case "$1 $2" in
 'container inspect'|'network inspect') if [ "$3" = '-f' ]; then echo 11111111-1111-1111-1111-111111111111; fi; exit 0;;
esac
if [ "$1" = inspect ]; then
 case "$3" in *io.cuchi.server*) echo 11111111-1111-1111-1111-111111111111;; *Config.Image*) echo test-image;; esac
fi
if [ "$1" = run ] && [ "$TEST_CASE" != success ]; then exit 1; fi
if [[ "$*" = *'/usr/local/bin/cuchi-egress direct' ]] && [ "$TEST_CASE" = rollback_failed ]; then exit 1; fi
if [[ "$*" = *'awg --version' ]]; then echo 3.1.20260812; fi
if [[ "$*" = *'amneziawg-go --version' ]]; then echo 3.1.20260828; fi
exit 0
`
			write(filepath.Join(dir, "docker"), docker)
			write(filepath.Join(dir, "flock"), "#!/bin/sh\nexit 0\n")
			write(filepath.Join(dir, "modprobe"), "#!/bin/sh\n[ \"$TEST_CASE\" != module_missing ]\n")
			var input bytes.Buffer
			tw := tar.NewWriter(&input)
			for name, data := range map[string]string{"egress-mode": desired, "warp.conf": "ephemeral fixture", "warp-address": "172.16.0.2/32", "awg0.conf": "fixture", "with-probe.conf": "fixture", "probe.conf": "fixture", "peers": ""} {
				tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(data))})
				tw.Write([]byte(data))
			}
			tw.Close()
			script := strings.ReplaceAll(apply, "/opt/cu-chi/", dir+"/")
			script = strings.ReplaceAll(script, "/run/lock/cuchi-awg.lock", filepath.Join(dir, "lock"))
			cmd := exec.Command("bash", "-c", script, "--", id, "test-image", "51820", "1", "sync")
			cmd.Stdin = &input
			cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "TEST_CASE="+scenario, "TEST_LOG="+filepath.Join(dir, "calls"))
			out, err := cmd.CombinedOutput()
			if (err == nil) != (scenario == "success") {
				t.Fatalf("unexpected apply result: %v %s", err, out)
			}
			want := old
			if scenario == "success" {
				want = desired
			}
			mode, _ := os.ReadFile(filepath.Join(base, "config/egress-mode"))
			if strings.TrimSpace(string(mode)) != want {
				t.Fatalf("mode %q, want %q", mode, want)
			}
			if scenario == "rollback_failed" && !strings.Contains(string(out), "CUCHI_ERROR_egress_rollback_failed") {
				t.Fatal("rollback failure hidden")
			}
			if scenario == "success" {
				if _, err := os.Stat(filepath.Join(base, "generation")); err != nil {
					t.Fatal("success not fenced")
				}
			} else if _, err := os.Stat(filepath.Join(base, "generation")); err == nil {
				t.Fatal("failure committed generation")
			}
			calls, _ := os.ReadFile(filepath.Join(dir, "calls"))
			if scenario == "failed_enable" && !strings.Contains(string(calls), "cuchi-egress direct") {
				t.Fatal("previous mode not restored")
			}
			for _, name := range []string{"config/with-probe.conf", "probe/probe.conf"} {
				if _, err := os.Stat(filepath.Join(base, name)); err == nil {
					t.Fatal("probe secret survived cleanup")
				}
			}
		})
	}
}
