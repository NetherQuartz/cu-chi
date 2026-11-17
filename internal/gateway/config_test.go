package gateway

import (
	"github.com/NetherQuartz/cu-chi/internal/awg"
	"strings"
	"testing"
)

func TestUplinkDialectAndNoHooks(t *testing.T) {
	profile := awg.NewSettings().Client(awg.NewKeys(), "10.66.0.2", "vpn.example", 51820)
	u, err := ParseUplink(profile)
	if err != nil {
		t.Fatal(err)
	}
	if u.Address != "10.66.0.2/32" || strings.Contains(u.Config, "DNS =") || strings.Contains(u.Config, "::/0") {
		t.Fatal("unexpected runtime configuration")
	}
	for name, value := range map[string]string{
		"hook":               strings.Replace(profile, "[Peer]", "PostUp = touch /tmp/unsafe\n[Peer]", 1),
		"duplicate":          profile + "\n[Peer]\nPublicKey = invalid\n",
		"wrong network":      strings.Replace(profile, "10.66.0.2/32", "192.168.0.1/24", 1),
		"route override":     strings.Replace(profile, "MTU = 1280", "MTU = 1280\nTable = main", 1),
		"endpoint injection": strings.Replace(profile, "vpn.example:51820", "$(id):51820", 1),
		"unknown":            strings.Replace(profile, "Jc = 4", "X = 4", 1),
		"old dialect":        strings.Replace(profile, "RandomTrailers = 1\n", "", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseUplink(value); err == nil {
				t.Fatal("unsafe profile accepted")
			}
		})
	}
}
func TestLANIdentityAndOverlap(t *testing.T) {
	lan, err := NewLAN("192.168.50.10", "192.168.50.1", "192.168.50.0/24")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(lan.RouterConfig, "Jc") || !strings.Contains(lan.ServerConfig, "10.77.0.2/32, 192.168.50.0/24") || !strings.Contains(lan.RouterConfig, "192.168.50.10:51820") {
		t.Fatal("invalid standard WG configuration")
	}
	for _, c := range []string{"0.0.0.0/0", "10.66.0.0/16", "10.77.0.0/24", "192.168.50.2/24", "192.168.0.0/15", "::/0", "192.168.0.0/24;id"} {
		if _, err := NewLAN("192.168.50.10", "192.168.50.1", c); err == nil {
			t.Fatalf("accepted %q", c)
		}
	}
	if _, err := NewLAN("203.0.113.10", "192.168.50.1", ""); err == nil {
		t.Fatal("public VM accepted")
	}
}
