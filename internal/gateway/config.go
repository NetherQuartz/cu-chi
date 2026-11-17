// Package gateway prepares configurations for the isolated LAN WireGuard bridge.
package gateway

import (
	"bufio"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"

	"github.com/NetherQuartz/cu-chi/internal/awg"
)

const Network = "10.77.0.0/24"
const Address = "10.77.0.1/24"
const RouterAddress = "10.77.0.2/32"

// Uplink accepts only the exported Củ Chi profile dialect. In particular it never
// executes wg-quick hooks, saves configuration, or changes host routing/DNS.
type Uplink struct{ Config, Address string }

func ParseUplink(data string) (Uplink, error) {
	fail := func() (Uplink, error) {
		return Uplink{}, errors.New("unsupported uplink profile; use a fresh Củ Chi AmneziaWG export (no hooks or extra peers)")
	}
	if len(data) > 16384 {
		return fail()
	}
	section := ""
	fields := map[string]map[string]string{"Interface": {}, "Peer": {}}
	seen := map[string]bool{}
	scanner := bufio.NewScanner(strings.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			if line != "[Interface]" && line != "[Peer]" {
				return fail()
			}
			section = strings.Trim(line, "[]")
			if seen[section] || (section == "Peer" && !seen["Interface"]) {
				return fail()
			}
			seen[section] = true
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if !ok || section == "" || value == "" || strings.ContainsAny(value, "\x00\r") {
			return fail()
		}
		if _, ok = fields[section][key]; ok {
			return fail()
		}
		fields[section][key] = value
	}
	if scanner.Err() != nil || !seen["Interface"] || !seen["Peer"] {
		return fail()
	}
	iface, peer := fields["Interface"], fields["Peer"]
	addr, err := netip.ParsePrefix(iface["Address"])
	if err != nil || !addr.Addr().Is4() || addr.Bits() != 32 || !netip.MustParsePrefix(awg.Network).Contains(addr.Addr()) || addr.Addr() == netip.MustParseAddr("10.66.0.1") {
		return fail()
	}
	if !keyOK(iface["PrivateKey"]) || !keyOK(iface["HeaderProtectionKey"]) || !keyOK(peer["PublicKey"]) || !keyOK(peer["PresharedKey"]) {
		return fail()
	}
	if iface["MTU"] != "1280" || iface["DNS"] != "1.1.1.1, 1.0.0.1" || peer["AllowedIPs"] != "0.0.0.0/0, ::/0" {
		return fail()
	}
	host, port, err := net.SplitHostPort(peer["Endpoint"])
	n, e := strconv.Atoi(port)
	if err != nil || e != nil || n < 1 || n > 65535 || host == "" {
		return fail()
	}
	// Endpoint is resolved by AWG, not interpolated into a shell command.
	for _, r := range host {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-') {
			return fail()
		}
	}
	var b strings.Builder
	b.WriteString("[Interface]\n")
	interfaceKeys := []string{"PrivateKey", "Jc", "Jmin", "Jmax", "S1", "H1", "S2", "H2", "S3", "H3", "S4", "H4", "HeaderProtectionKey", "ContentPaddingAddition", "RekeyAfterTime", "RekeyTimeout", "RejectAfterTime", "KeepaliveTimeout", "MaxHandshakeAttempts", "RandomTrailers"}
	allowed := map[string]bool{"Address": true, "DNS": true, "MTU": true}
	for _, key := range interfaceKeys {
		value, ok := iface[key]
		if !ok {
			return fail()
		}
		allowed[key] = true
		if key != "PrivateKey" && key != "HeaderProtectionKey" && !numericRange(value) {
			return fail()
		}
		fmt.Fprintf(&b, "%s = %s\n", key, value)
	}
	for key := range iface {
		if !allowed[key] {
			return fail()
		}
	}
	if len(peer) != 5 || !numericRange(peer["PersistentKeepalive"]) {
		return fail()
	}
	fmt.Fprintf(&b, "\n[Peer]\nPublicKey = %s\nPresharedKey = %s\nEndpoint = %s\nAllowedIPs = 0.0.0.0/0\nPersistentKeepalive = %s\n", peer["PublicKey"], peer["PresharedKey"], peer["Endpoint"], peer["PersistentKeepalive"])
	return Uplink{b.String(), addr.String()}, nil
}
func keyOK(s string) bool {
	b, e := base64.StdEncoding.DecodeString(s)
	return e == nil && len(b) == 32
}
func numericRange(s string) bool {
	parts := strings.Split(s, "-")
	if len(parts) > 2 {
		return false
	}
	var previous uint64
	for i, p := range parts {
		n, e := strconv.ParseUint(p, 10, 32)
		if e != nil || (i > 0 && n < previous) {
			return false
		}
		previous = n
	}
	return true
}

type LAN struct{ ServerConfig, RouterConfig string }

func NewLAN(endpoint, router string, cidrs string) (LAN, error) {
	vm, err := netip.ParseAddr(endpoint)
	r, re := netip.ParseAddr(router)
	reserved := []netip.Prefix{netip.MustParsePrefix(Network), netip.MustParsePrefix(awg.Network)}
	if err != nil || re != nil || !vm.Is4() || !r.Is4() || !vm.IsPrivate() || !r.IsPrivate() || vm == r {
		return LAN{}, errors.New("gateway and router must be distinct private IPv4 addresses")
	}
	for _, p := range reserved {
		if p.Contains(vm) || p.Contains(r) {
			return LAN{}, errors.New("LAN addresses overlap tunnel networks")
		}
	}
	allowed := []string{RouterAddress}
	if cidrs != "" {
		for _, raw := range strings.Split(cidrs, ",") {
			p, e := netip.ParsePrefix(strings.TrimSpace(raw))
			if e != nil || !p.Addr().Is4() || p != p.Masked() || p.Bits() < 16 || !p.Addr().IsPrivate() {
				return LAN{}, errors.New("client networks must be private IPv4 networks with prefix /16 or narrower")
			}
			for _, network := range reserved {
				if p.Overlaps(network) {
					return LAN{}, errors.New("client network overlaps a tunnel network")
				}
			}
			allowed = append(allowed, p.String())
		}
	}
	server, client := awg.NewKeys(), awg.NewKeys()
	return LAN{
		fmt.Sprintf("[Interface]\nPrivateKey = %s\nListenPort = 51820\n\n[Peer]\nPublicKey = %s\nPresharedKey = %s\nAllowedIPs = %s\n", server.Private, client.Public, client.PSK, strings.Join(allowed, ", ")),
		fmt.Sprintf("[Interface]\nPrivateKey = %s\nAddress = %s\nDNS = 1.1.1.1, 1.0.0.1\nMTU = 1280\n\n[Peer]\nPublicKey = %s\nPresharedKey = %s\nEndpoint = %s:51820\nAllowedIPs = 0.0.0.0/0\nPersistentKeepalive = 25\n", client.Private, RouterAddress, server.Public, client.PSK, vm),
	}, nil
}
