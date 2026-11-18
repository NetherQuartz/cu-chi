// Package egress validates the deliberately restricted WARP WireGuard dialect.
// It never passes wg-quick hooks, routing directives or untrusted shell text through.
package egress

import (
	"bufio"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

var ErrConfig = errors.New("invalid WARP configuration")

type Config struct {
	WireGuard string
	Address   string
}

func Parse(raw string) (Config, error) {
	if len(raw) > 8192 {
		return Config{}, ErrConfig
	}
	fields := map[string]map[string]string{"Interface": {}, "Peer": {}}
	seen := map[string]bool{}
	section := ""
	scan := bufio.NewScanner(strings.NewReader(raw))
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			section = strings.Trim(line, "[]")
			if fields[section] == nil || seen[section] || line != "["+section+"]" || section == "Peer" && !seen["Interface"] {
				return Config{}, ErrConfig
			}
			seen[section] = true
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if !ok || section == "" || v == "" || strings.ContainsAny(v, "\x00\r") {
			return Config{}, ErrConfig
		}
		if _, ok = fields[section][k]; ok {
			return Config{}, ErrConfig
		}
		fields[section][k] = v
	}
	if scan.Err() != nil || !seen["Interface"] || !seen["Peer"] {
		return Config{}, ErrConfig
	}
	iface, peer := fields["Interface"], fields["Peer"]
	for k := range iface {
		if k != "PrivateKey" && k != "Address" && k != "DNS" && k != "MTU" {
			return Config{}, ErrConfig
		}
	}
	for k := range peer {
		if k != "PublicKey" && k != "AllowedIPs" && k != "Endpoint" && k != "PersistentKeepalive" {
			return Config{}, ErrConfig
		}
	}
	for _, key := range []string{iface["PrivateKey"], peer["PublicKey"]} {
		b, e := base64.StdEncoding.DecodeString(key)
		if e != nil || len(b) != 32 {
			return Config{}, ErrConfig
		}
	}
	address := ""
	for _, v := range strings.Split(iface["Address"], ",") {
		p, e := netip.ParsePrefix(strings.TrimSpace(v))
		if e != nil {
			return Config{}, ErrConfig
		}
		if p.Addr().Is4() {
			if address != "" || p.Bits() != 32 || !netip.MustParsePrefix("172.16.0.0/12").Contains(p.Addr()) {
				return Config{}, ErrConfig
			}
			address = p.String()
		} else if p.Bits() != 128 {
			return Config{}, ErrConfig
		}
	}
	if address == "" {
		return Config{}, ErrConfig
	}
	host, port, e := net.SplitHostPort(peer["Endpoint"])
	n, pe := strconv.Atoi(port)
	if e != nil || pe != nil || n < 1 || n > 65535 {
		return Config{}, ErrConfig
	}
	// wgcf currently exports this hostname or an IP in Cloudflare's WARP ranges.
	allowed := host == "engage.cloudflareclient.com"
	if ip, e := netip.ParseAddr(host); e == nil {
		allowed = netip.MustParsePrefix("162.159.192.0/24").Contains(ip) || netip.MustParsePrefix("162.159.193.0/24").Contains(ip)
	}
	if !allowed {
		return Config{}, ErrConfig
	}
	if peer["AllowedIPs"] != "0.0.0.0/0, ::/0" && peer["AllowedIPs"] != "0.0.0.0/0" {
		return Config{}, ErrConfig
	}
	// IPv6 stays blocked, consistent with existing profiles; DNS stays inside AWG.
	conf := fmt.Sprintf("[Interface]\nPrivateKey = %s\n[Peer]\nPublicKey = %s\nEndpoint = %s\nAllowedIPs = 0.0.0.0/0\nPersistentKeepalive = 25\n", iface["PrivateKey"], peer["PublicKey"], peer["Endpoint"])
	return Config{conf, address}, nil
}
