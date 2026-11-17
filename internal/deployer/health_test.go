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

// Exercise the real embedded shell with a fake Docker boundary. This catches
// unintended peer reconciliation, leaked files, lock/generation and cleanup regressions.
func TestProbeScriptIsolationAndCleanup(t *testing.T) {
	for _, scenario := range []string{"success", "failure", "stale", "foreign", "cleanup_failure"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			id := "11111111-1111-1111-1111-111111111111"
			base := filepath.Join(dir, id)
			if err := os.MkdirAll(base, 0700); err != nil {
				t.Fatal(err)
			}
			write := func(p, s string, mode os.FileMode) {
				t.Helper()
				if err := os.WriteFile(p, []byte(s), mode); err != nil {
					t.Fatal(err)
				}
			}
			write(filepath.Join(base, "generation"), "1", 0600)
			docker := `#!/bin/bash
printf '%s\n' "$*" >> "$TEST_LOG"
if [ "$1 $2 $3" = 'container inspect cuchi-awg' ]; then exit 0; fi
if [ "$1 $2" = 'container inspect' ]; then exit 1; fi
if [ "$1" = inspect ]; then
 case "$3" in
 *io.cuchi.server*) if [ "$TEST_CASE" = foreign ]; then echo foreign; else echo 11111111-1111-1111-1111-111111111111; fi ;;
 *State.Running*) echo true;;
 *Config.Image*) printf 'ghcr.io/netherquartz/cu-chi-awg@sha256:%064d\n' 0;;
 esac
 exit 0
fi
if [ "$1 $2" = 'exec -i' ]; then cat >/dev/null; exit 0; fi
if [ "$1" = run ] && [ "$TEST_CASE" = failure ]; then exit 1; fi
if [[ "$*" = *' remove' ]] && [ "$TEST_CASE" = cleanup_failure ]; then exit 1; fi
exit 0
`
			write(filepath.Join(dir, "docker"), docker, 0700)
			write(filepath.Join(dir, "flock"), "#!/bin/sh\nexit 0\n", 0700)
			write(filepath.Join(dir, "timeout"), "#!/bin/sh\nshift 3\nexec \"$@\"\n", 0700)
			script := strings.ReplaceAll(healthScript, "/opt/cu-chi/", dir+"/")
			script = strings.ReplaceAll(script, "/run/lock/cuchi-awg.lock", filepath.Join(dir, "lock"))
			var input bytes.Buffer
			tw := tar.NewWriter(&input)
			for _, name := range []string{"psk", "probe.conf"} {
				data := "ephemeral-test-only"
				tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(data))})
				tw.Write([]byte(data))
			}
			tw.Close()
			generation := "1"
			if scenario == "stale" {
				generation = "2"
			}
			peer := strings.Repeat("A", 43) + "="
			cmd := exec.Command("bash", "-c", script, "--", id, generation, peer)
			cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "TEST_LOG="+filepath.Join(dir, "calls"), "TEST_CASE="+scenario)
			cmd.Stdin = &input
			out, err := cmd.CombinedOutput()
			if scenario == "foreign" || scenario == "cleanup_failure" {
				if err == nil {
					t.Fatal("unsafe success", string(out))
				}
			} else {
				if err != nil {
					t.Fatal(err, string(out))
				}
				want := map[string]string{"success": "HEALTH_OK", "failure": "HEALTH_DATA", "stale": "HEALTH_BUSY"}[scenario]
				if strings.TrimSpace(string(out)) != want {
					t.Fatal("result", string(out))
				}
			}
			calls, _ := os.ReadFile(filepath.Join(dir, "calls"))
			for _, forbidden := range []string{"syncconf", "setconf", "restart", "docker pull", "private-key", "showconf"} {
				if strings.Contains(string(calls), forbidden) {
					t.Fatal("changed unrelated runtime", forbidden)
				}
			}
			for _, name := range []string{"psk", "probe.conf"} {
				if _, err = os.Stat(filepath.Join(base, "health", name)); !os.IsNotExist(err) {
					t.Fatal("probe secret retained", name)
				}
			}
			if scenario == "success" || scenario == "failure" {
				if !strings.Contains(string(calls), "peer "+peer+" remove") {
					t.Fatal("peer not removed")
				}
				if _, err = os.Stat(filepath.Join(base, "health-peer")); !os.IsNotExist(err) {
					t.Fatal("journal retained after successful cleanup")
				}
			}
		})
	}
}
