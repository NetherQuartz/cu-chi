package deployer

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestMonitoringParserRejectsTruncatedAndSensitiveOutput(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	at := time.Unix(10000, 0)
	base := "BOOT test\nCPU 100 20\nCORES 2\nMEM 1000 400\nDISK 1000 500\nUPTIME 100\nNET eth0 1000 2000\nTUNNEL 1\nEPOCH start\nPEER " + key + " 5 8\nHANDSHAKE " + key + " 9999\nEND\n"
	s, err := parseObservation(base, at)
	if err != nil || !s.Tunnel || len(s.Peers) != 1 || s.Peers[0].RX != 5 {
		t.Fatal("valid response rejected", err)
	}
	for _, bad := range []string{strings.TrimSuffix(base, "END\n"), base + "PRIVATEKEY secret\n", strings.Replace(base, "MEM 1000 400", "MEM 1000 -4", 1), strings.Replace(base, "9999", "99999999", 1), strings.Replace(base, "UPTIME 100", "UPTIME NaN", 1), strings.Replace(base, "TUNNEL 1", "TUNNEL 0", 1), base + "BOOT duplicate\n"} {
		if _, err := parseObservation(bad, at); err == nil {
			t.Fatal("invalid response accepted")
		}
	}
}
