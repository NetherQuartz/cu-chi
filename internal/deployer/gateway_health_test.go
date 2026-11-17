package deployer

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestGatewayEgressWarmup(t *testing.T) {
	for _, tt := range []struct {
		name     string
		failures int
		reason   string
		want     string
		attempts int
	}{
		{"immediate", 0, "", "", 1},
		{"cold tunnel", 2, "CUCHI_ERROR_gateway_dns_failed", "", 3},
		{"persistent DNS", 9, "CUCHI_ERROR_gateway_dns_failed", "CUCHI_ERROR_gateway_dns_failed", 6},
		{"persistent HTTPS", 9, "CUCHI_ERROR_gateway_https_failed", "CUCHI_ERROR_gateway_https_failed", 6},
		{"runtime lost", 9, "CUCHI_ERROR_runtime_start_failed", "CUCHI_ERROR_runtime_start_failed", 6},
		{"redaction", 9, "private diagnostic must not escape", "CUCHI_ERROR_gateway_egress_failed", 6},
	} {
		t.Run(tt.name, func(t *testing.T) {
			counter := filepath.Join(t.TempDir(), "attempts")
			if err := os.WriteFile(counter, []byte("0"), 0600); err != nil {
				t.Fatal(err)
			}
			script := `docker() {
 n=$(cat "$COUNTER"); n=$((n+1)); printf '%s' "$n" > "$COUNTER"
 if [ "$n" -le "$FAILURES" ]; then printf '%s\n' "$REASON"; return 1; fi
 return 0
}
sleep() { :; }
` + gatewayHealthWait + "\nwait_for_gateway_egress"
			cmd := exec.Command("/bin/bash", "-c", script)
			cmd.Env = append(os.Environ(), "COUNTER="+counter, "FAILURES="+strconv.Itoa(tt.failures), "REASON="+tt.reason)
			out, err := cmd.CombinedOutput()
			if (err != nil) != (tt.want != "") || strings.TrimSpace(string(out)) != tt.want {
				t.Fatalf("result %q, %v", out, err)
			}
			n, e := os.ReadFile(counter)
			if e != nil || string(n) != strconv.Itoa(tt.attempts) {
				t.Fatalf("attempt count %q: %v", n, e)
			}
		})
	}
}
