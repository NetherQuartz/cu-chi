// Package push implements bounded Web Push delivery to known public push services.
package push

import (
	"context"
	"crypto/ecdh"
	"encoding/base64"
	"encoding/json"
	"errors"
	webpush "github.com/SherClockHolmes/webpush-go"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

var ErrInvalid = errors.New("invalid push subscription")

func AllowedHost(host string) bool {
	return host == "fcm.googleapis.com" || host == "updates.push.services.mozilla.com" || strings.HasSuffix(host, ".push.services.mozilla.com") || strings.HasSuffix(host, ".push.apple.com")
}
func Validate(s webpush.Subscription) error {
	u, err := url.Parse(s.Endpoint)
	if err != nil || len(s.Endpoint) > 4096 || u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.Opaque != "" || u.Port() != "" && u.Port() != "443" || !AllowedHost(u.Hostname()) || u.Path == "" {
		return ErrInvalid
	}
	key, err := base64.RawURLEncoding.DecodeString(s.Keys.P256dh)
	if err != nil {
		return ErrInvalid
	}
	if _, err = ecdh.P256().NewPublicKey(key); err != nil {
		return ErrInvalid
	}
	auth, err := base64.RawURLEncoding.DecodeString(s.Keys.Auth)
	if err != nil || len(auth) != 16 {
		return ErrInvalid
	}
	return nil
}
func publicIP(ip net.IP) bool {
	a, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	a = a.Unmap()
	if !a.IsGlobalUnicast() || a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() {
		return false
	}
	for _, cidr := range []string{"100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "64:ff9b::/96", "2002::/16"} {
		if netip.MustParsePrefix(cidr).Contains(a) {
			return false
		}
	}
	return true
}
func Client() *http.Client {
	return &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("push redirects forbidden") }, Transport: &http.Transport{
		Proxy: nil, MaxResponseHeaderBytes: 16 << 10, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 10 * time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil || port != "443" || !AllowedHost(host) {
				return nil, ErrInvalid
			}
			addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, ErrInvalid
			}
			for _, ip := range addresses {
				if !publicIP(ip.IP) {
					return nil, ErrInvalid
				}
			}
			for _, ip := range addresses {
				c, e := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(ip.IP.String(), port))
				if e == nil {
					return c, nil
				}
			}
			return nil, errors.New("push service unavailable")
		},
	}}
}
func Payload(id, kind, severity string) []byte {
	body := map[string]string{"outage": "Обнаружена проблема с VPN. Подробности в личном кабинете.", "management": "Не удаётся проверить сервер. Откройте панель администратора.", "recovery": "Проверка VPN снова проходит успешно.", "reminder": "Доступен новый основной сервер. Перевыпустите и импортируйте профили устройств.", "test": "Уведомления Củ Chi подключены.", "job_failed": "Изменение VPN не удалось применить. Откройте панель администратора."}[kind]
	title := "Củ Chi"
	if severity == "critical" {
		title += " · Критично"
	}
	b, _ := json.Marshal(map[string]string{"id": id, "title": title, "body": body, "severity": severity})
	return b
}
func Send(ctx context.Context, client *http.Client, s webpush.Subscription, private, public, origin, id, kind, severity string, ttl int) (int, error) {
	if err := Validate(s); err != nil {
		return 0, err
	}
	urgency := webpush.UrgencyNormal
	if severity == "critical" {
		urgency = webpush.UrgencyHigh
	}
	resp, err := webpush.SendNotificationWithContext(ctx, Payload(id, kind, severity), &s, &webpush.Options{HTTPClient: client, Subscriber: origin, VAPIDPrivateKey: private, VAPIDPublicKey: public, TTL: ttl, Urgency: urgency, Topic: strings.ReplaceAll(id, "-", "")})
	if err != nil {
		return 0, errors.New("push transport failed")
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}
