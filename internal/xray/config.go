// Package xray renders the server, synthetic client, and device import link
// from the same REALITY identity.
package xray

import (
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
)

const Image = "ghcr.io/xtls/xray-core@sha256:9812e382e229e9ed63fd179bf1a4e3fe244c12ff3ae4fd8128745d79c87575ab" // v26.11.9

type Identity struct{ Private, Public, Short string }

func Generate() Identity {
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	var short [8]byte
	rand.Read(short[:])
	return Identity{base64.RawURLEncoding.EncodeToString(k.Bytes()), base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes()), hex.EncodeToString(short[:])}
}
func ClientID(seed []byte, server string) string {
	m := hmac.New(sha256.New, seed)
	m.Write([]byte("xray-vless:" + server))
	b := m.Sum(nil)
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
func Link(id, host, sni, pub, short, label string) string {
	q := url.Values{"encryption": {"none"}, "security": {"reality"}, "type": {"tcp"}, "flow": {"xtls-rprx-vision"}, "sni": {sni}, "fp": {"chrome"}, "pbk": {pub}, "sid": {short}}
	return (&url.URL{Scheme: "vless", User: url.User(id), Host: net.JoinHostPort(host, "443"), RawQuery: q.Encode(), Fragment: label}).String()
}

type object = map[string]any

func Configs(k Identity, sni, probe string, clients []string) ([]byte, []byte, error) {
	users := []object{}
	for _, id := range append(clients, probe) {
		users = append(users, object{"id": id, "flow": "xtls-rprx-vision"})
	}
	transport := func(reality object) object {
		return object{"network": "raw", "security": "reality", "realitySettings": reality}
	}
	inbound := object{"listen": "0.0.0.0", "port": 8443, "protocol": "vless", "settings": object{"clients": users, "decryption": "none"}, "streamSettings": transport(object{"target": net.JoinHostPort(sni, "443"), "serverNames": []string{sni}, "privateKey": k.Private, "shortIds": []string{k.Short}})}
	server := object{"log": object{"loglevel": "warning", "access": "none"}, "inbounds": []object{inbound}, "outbounds": []object{{"protocol": "freedom", "settings": object{"domainStrategy": "UseIP"}}, {"tag": "deny", "protocol": "blackhole"}}, "routing": object{"domainStrategy": "IPOnDemand", "rules": []object{{"type": "field", "ip": []string{"geoip:private"}, "outboundTag": "deny"}}}}
	outbound := object{"protocol": "vless", "settings": object{"vnext": []object{{"address": "127.0.0.1", "port": 8443, "users": []object{{"id": probe, "encryption": "none", "flow": "xtls-rprx-vision"}}}}}, "streamSettings": transport(object{"serverName": sni, "fingerprint": "chrome", "password": k.Public, "shortId": k.Short})}
	client := object{"inbounds": []object{{"listen": "127.0.0.1", "port": 1080, "protocol": "socks"}}, "outbounds": []object{outbound}}
	a, e := json.Marshal(server)
	if e != nil {
		return nil, nil, e
	}
	b, e := json.Marshal(client)
	return a, b, e
}
