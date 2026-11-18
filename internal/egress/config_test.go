package egress

import (
	"github.com/NetherQuartz/cu-chi/internal/awg"
	"strings"
	"testing"
)

func TestParseRestrictedProfile(t *testing.T) {
	key, peer := awg.NewKeys(), awg.NewKeys()
	raw := "[Interface]\nPrivateKey = " + key.Private + "\nAddress = 172.16.0.2/32, 2606:4700:110:1234::2/128\nDNS = 1.1.1.1\nMTU = 1280\n[Peer]\nPublicKey = " + peer.Public + "\nAllowedIPs = 0.0.0.0/0, ::/0\nEndpoint = engage.cloudflareclient.com:2408\n"
	c, err := Parse(raw)
	if err != nil || c.Address != "172.16.0.2/32" {
		t.Fatal("valid registration rejected", err)
	}
	if strings.Contains(c.WireGuard, "::/0") || strings.Contains(c.WireGuard, "DNS") || !strings.Contains(c.WireGuard, "PersistentKeepalive = 25") {
		t.Fatal("unsafe normalization")
	}
	for name, bad := range map[string]string{
		"hook":             strings.Replace(raw, "[Peer]", "PostUp = echo secret\n[Peer]", 1),
		"duplicate":        raw + "[Peer]\n",
		"duplicate_field":  raw + "Endpoint = engage.cloudflareclient.com:2408\n",
		"private_endpoint": strings.Replace(raw, "engage.cloudflareclient.com", "127.0.0.1", 1),
		"public_endpoint":  strings.Replace(raw, "engage.cloudflareclient.com", "example.com", 1),
		"shell":            strings.Replace(raw, ":2408", ":2408;echo secret", 1),
		"address":          strings.Replace(raw, "172.16.0.2/32", "10.66.0.2/32", 1),
		"wide_address":     strings.Replace(raw, "172.16.0.2/32", "172.16.0.2/12", 1),
		"route":            strings.Replace(raw, "0.0.0.0/0, ::/0", "10.0.0.0/8", 1),
		"key":              strings.Replace(raw, key.Private, "secret", 1),
		"oversize":         raw + strings.Repeat("#", 8192),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(bad); err != ErrConfig {
				t.Fatal("accepted invalid profile or exposed nonconstant error")
			}
		})
	}
}
