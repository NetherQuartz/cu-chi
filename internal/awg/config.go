// Package awg renders the supported AmneziaWG 3.1 configuration dialect.
package awg

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"strings"
)

const Version = "3.1.20260828"
const ToolsVersion = "3.1.20260812"
const Network = "10.66.0.0/16"
const ProbeAddress = "10.66.255.254"

type Keys struct {
	Private string `json:"private"`
	Public  string `json:"public"`
	PSK     string `json:"psk"`
}
type Settings struct {
	Keys      Keys      `json:"keys"`
	HeaderKey string    `json:"header_key"`
	Headers   [4]uint32 `json:"headers"`
	Paddings  [4]int    `json:"paddings"`
}
type Peer struct{ Public, PSK, Address string }

func NewKeys() Keys {
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	b := key.Bytes()
	b[0] &= 248
	b[31] = (b[31] & 127) | 64
	return Keys{base64.StdEncoding.EncodeToString(b), base64.StdEncoding.EncodeToString(key.PublicKey().Bytes()), randomKey()}
}
func randomKey() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.StdEncoding.EncodeToString(b)
}
func NewSettings() Settings {
	s := Settings{Keys: NewKeys(), HeaderKey: randomKey()}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	// Separate quarters prevent header collisions; every server gets its own values.
	for i := range 4 {
		s.Headers[i] = uint32(i+1)*800000000 + uint32(b[i])*100000 + uint32(b[i+4])*100
		s.Paddings[i] = 16 + int(b[i]%33)
	}
	return s
}
func Marshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
func (s Settings) Parameters() string {
	var b strings.Builder
	for i := range 4 {
		fmt.Fprintf(&b, "S%d = %d\nH%d = %d-%d\n", i+1, s.Paddings[i], i+1, s.Headers[i], s.Headers[i]+99)
	}
	fmt.Fprintf(&b, "HeaderProtectionKey = %s\nContentPaddingAddition = 0-32\nRekeyAfterTime = 100-140\nRekeyTimeout = 4-7\nRejectAfterTime = 180-210\nKeepaliveTimeout = 10-15\nMaxHandshakeAttempts = 18-24\nRandomTrailers = 1\n", s.HeaderKey)
	return b.String()
}
func (s Settings) Server(peers []Peer) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[Interface]\nPrivateKey = %s\nListenPort = 51820\n%s", s.Keys.Private, s.Parameters())
	for _, p := range peers {
		fmt.Fprintf(&b, "\n[Peer]\nPublicKey = %s\nPresharedKey = %s\nAllowedIPs = %s/32\n", p.Public, p.PSK, p.Address)
	}
	return b.String()
}
func (s Settings) Client(k Keys, address, endpoint string, port int) string {
	return fmt.Sprintf("[Interface]\nPrivateKey = %s\nAddress = %s/32\nDNS = 1.1.1.1, 1.0.0.1\nMTU = 1280\nJc = 4\nJmin = 40\nJmax = 100\n%s\n[Peer]\nPublicKey = %s\nPresharedKey = %s\nEndpoint = %s\nAllowedIPs = 0.0.0.0/0, ::/0\nPersistentKeepalive = 22-30\n", k.Private, address, s.Parameters(), s.Keys.Public, k.PSK, net.JoinHostPort(endpoint, fmt.Sprint(port)))
}

// Probe is an awg setconf file: routing is installed by the probe container.
func (s Settings) Probe(k Keys) string {
	return fmt.Sprintf("[Interface]\nPrivateKey = %s\n%s\n[Peer]\nPublicKey = %s\nPresharedKey = %s\nEndpoint = cuchi-awg:51820\nAllowedIPs = 0.0.0.0/0\nPersistentKeepalive = 1\n", k.Private, s.Parameters(), s.Keys.Public, k.PSK)
}
