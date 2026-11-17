package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/NetherQuartz/cu-chi/internal/deployer"
)

type Config struct {
	Listen, PublicURL, DatabaseURL                        string
	MasterKey                                             []byte
	DevAuth                                               bool
	AWGImage                                              string
	Issuer, ClientID, ClientSecret, AdminGroup, UserGroup string
}

func Load() (Config, error) {
	c := Config{Listen: value("CUCHI_LISTEN", "127.0.0.1:8080"), PublicURL: strings.TrimRight(value("CUCHI_PUBLIC_URL", "http://127.0.0.1:8080"), "/"), DatabaseURL: os.Getenv("CUCHI_DATABASE_URL"), DevAuth: os.Getenv("CUCHI_DEV_AUTH") == "true", Issuer: strings.TrimRight(os.Getenv("CUCHI_OIDC_ISSUER"), "/"), ClientID: os.Getenv("CUCHI_OIDC_CLIENT_ID"), ClientSecret: os.Getenv("CUCHI_OIDC_CLIENT_SECRET"), AdminGroup: value("CUCHI_ADMIN_GROUP", "administrators"), UserGroup: value("CUCHI_USER_GROUP", "vpn-users")}
	c.AWGImage = value("CUCHI_AWG_IMAGE", deployer.RuntimeImage)
	if c.AWGImage != "" && !deployer.ValidImage(c.AWGImage) {
		return c, errors.New("CUCHI_AWG_IMAGE must be a pinned official Củ Chi runtime digest")
	}
	var err error
	c.MasterKey, err = base64.StdEncoding.DecodeString(os.Getenv("CUCHI_MASTER_KEY"))
	if err != nil || len(c.MasterKey) != 32 {
		return c, errors.New("CUCHI_MASTER_KEY must be a base64-encoded 32-byte key")
	}
	if c.DatabaseURL == "" {
		return c, errors.New("CUCHI_DATABASE_URL is required")
	}
	u, err := url.Parse(c.PublicURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return c, errors.New("CUCHI_PUBLIC_URL must be an origin without a path, credentials, query or fragment")
	}
	host, _, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return c, fmt.Errorf("CUCHI_LISTEN: %w", err)
	}
	if c.DevAuth {
		if !loopback(host) || !loopback(u.Hostname()) || u.Scheme != "http" || u.Host != c.Listen {
			return c, errors.New("development authentication requires matching loopback HTTP listen and public URL")
		}
	} else {
		if u.Scheme != "https" {
			return c, errors.New("production requires an HTTPS CUCHI_PUBLIC_URL")
		}
		issuer, e := url.Parse(c.Issuer)
		if e != nil || issuer.Scheme != "https" || issuer.Host == "" || issuer.User != nil || issuer.RawQuery != "" || issuer.Fragment != "" || c.ClientID == "" || c.ClientSecret == "" {
			return c, errors.New("production requires HTTPS OIDC issuer, client ID and client secret")
		}
	}
	return c, nil
}
func value(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}
func loopback(host string) bool { ip := net.ParseIP(host); return ip != nil && ip.IsLoopback() }
