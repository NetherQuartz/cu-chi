package deployer

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/subtle"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// RuntimeImage is set to a tested, published digest by GitHub CI.
var RuntimeImage string

//go:embed preflight.sh
var preflight string

//go:embed apply.sh
var apply string
var imageRE = regexp.MustCompile(`^ghcr\.io/netherquartz/cu-chi-awg@sha256:[a-f0-9]{64}$`)
var idRE = regexp.MustCompile(`^[a-f0-9-]{36}$`)

func ValidImage(s string) bool { return imageRE.MatchString(s) }

type Credentials struct {
	Host, User, Fingerprint, AuthMethod string
	Port                                int
	Key, Password                       []byte
}
type Payload struct {
	ServerID, Image          string
	Port                     int
	Generation               int64
	Config, WithProbe, Probe string
	PublicKeys               []string
	Install                  bool
}
type SSH struct{}

var ErrHostKey = errors.New("SSH host key fingerprint mismatch")

func PinnedHostKey(want string) ssh.HostKeyCallback {
	return func(_ string, _ net.Addr, key ssh.PublicKey) error {
		if subtle.ConstantTimeCompare([]byte(ssh.FingerprintSHA256(key)), []byte(want)) != 1 {
			return ErrHostKey
		}
		return nil
	}
}
func dial(ctx context.Context, c Credentials) (*ssh.Client, error) {
	var auth ssh.AuthMethod
	switch c.AuthMethod {
	case "", "key":
		signer, err := ssh.ParsePrivateKey(c.Key)
		if err != nil {
			return nil, errors.New("SSH private key invalid")
		}
		auth = ssh.PublicKeys(signer)
	case "password":
		if len(c.Password) == 0 {
			return nil, errors.New("SSH password missing")
		}
		auth = ssh.Password(string(c.Password))
	default:
		return nil, errors.New("SSH authentication method invalid")
	}
	d := net.Dialer{Timeout: 10 * time.Second}
	addr := net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	raw, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, errors.New("SSH connection failed")
	}
	raw.SetDeadline(time.Now().Add(15 * time.Second))
	stop := context.AfterFunc(ctx, func() { raw.Close() })
	defer stop()
	cfg := &ssh.ClientConfig{User: c.User, Auth: []ssh.AuthMethod{auth}, HostKeyCallback: PinnedHostKey(c.Fingerprint)}
	conn, chans, reqs, err := ssh.NewClientConn(raw, addr, cfg)
	if err != nil {
		raw.Close()
		if errors.Is(err, ErrHostKey) {
			return nil, ErrHostKey
		}
		return nil, errors.New("SSH authentication or handshake failed")
	}
	raw.SetDeadline(time.Time{})
	return ssh.NewClient(conn, chans, reqs), nil
}
func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

type limitedOutput struct{ b bytes.Buffer }

func (w *limitedOutput) Write(p []byte) (int, error) {
	n := len(p)
	if w.b.Len() < 16384 {
		remaining := 16384 - w.b.Len()
		if len(p) > remaining {
			p = p[:remaining]
		}
		w.b.Write(p)
	}
	return n, nil
}
func run(ctx context.Context, c *ssh.Client, user, script string, args []string, input []byte) (string, error) {
	session, err := c.NewSession()
	if err != nil {
		return "", errors.New("SSH session failed")
	}
	defer session.Close()
	prefix := ""
	if user != "root" {
		prefix = "sudo -n "
	}
	cmd := prefix + "/bin/bash -c " + quote(script) + " --"
	for _, a := range args {
		cmd += " " + quote(a)
	}
	session.Stdin = bytes.NewReader(input)
	var out limitedOutput
	session.Stdout = &out
	session.Stderr = io.Discard
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	err = session.Run(cmd)
	if err != nil {
		for _, code := range []string{"root_required", "ubuntu_lts_required", "amd64_required", "tun_unavailable", "active_ufw", "foreign_container", "foreign_network", "stale_generation", "runtime_start_failed"} {
			if strings.Contains(out.b.String(), "CUCHI_ERROR_"+code) {
				return "", errors.New(code)
			}
		}
		if ctx.Err() != nil {
			return "", errors.New("SSH operation timed out or cancelled")
		}
		return "", errors.New("remote command failed; check VPS disk, Docker, network and provider firewall")
	}
	return out.b.String(), nil
}
func (SSH) Check(ctx context.Context, c Credentials) error {
	client, err := dial(ctx, c)
	if err != nil {
		return err
	}
	defer client.Close()
	out, err := run(ctx, client, c.User, preflight, nil, nil)
	if err == nil && !strings.Contains(out, "CUCHI_PREFLIGHT_OK") {
		return errors.New("preflight response missing")
	}
	return err
}
func (SSH) Apply(ctx context.Context, c Credentials, p Payload) error {
	if !idRE.MatchString(p.ServerID) || !ValidImage(p.Image) || p.Port < 1 || p.Port > 65535 || p.Generation < 1 {
		return errors.New("invalid deployment configuration")
	}
	client, err := dial(ctx, c)
	if err != nil {
		return err
	}
	defer client.Close()
	pre, err := run(ctx, client, c.User, preflight, nil, nil)
	if err != nil {
		return err
	}
	if !strings.Contains(pre, "CUCHI_PREFLIGHT_OK") {
		return errors.New("preflight response missing")
	}
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	for _, f := range []struct{ name, data string }{{"awg0.conf", p.Config}, {"with-probe.conf", p.WithProbe}, {"probe.conf", p.Probe}, {"peers", strings.Join(p.PublicKeys, "\n") + trailing(p.PublicKeys)}} {
		if err = tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0600, Size: int64(len(f.data))}); err != nil {
			return err
		}
		if _, err = tw.Write([]byte(f.data)); err != nil {
			return err
		}
	}
	if err = tw.Close(); err != nil {
		return err
	}
	mode := "sync"
	if p.Install {
		mode = "deploy"
	}
	out, err := run(ctx, client, c.User, apply, []string{p.ServerID, p.Image, strconv.Itoa(p.Port), fmt.Sprint(p.Generation), mode}, b.Bytes())
	if err == nil && !strings.Contains(out, "CUCHI_APPLY_OK") {
		return errors.New("deployment acknowledgement missing")
	}
	return err
}
func trailing(v []string) string {
	if len(v) > 0 {
		return "\n"
	}
	return ""
}

// DiscoverHostFingerprint observes the first host key without sending credentials.
// The caller must persist it before any authenticated connection (TOFU).
func DiscoverHostFingerprint(ctx context.Context, host string, port int) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	raw, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return "", errors.New("SSH host discovery connection failed")
	}
	defer raw.Close()
	raw.SetDeadline(time.Now().Add(15 * time.Second))
	stop := context.AfterFunc(ctx, func() { raw.Close() })
	defer stop()
	observed := errors.New("SSH host key observed")
	var fingerprint string
	cfg := &ssh.ClientConfig{User: "cuchi-host-check", HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
		fingerprint = ssh.FingerprintSHA256(key)
		return observed
	}}
	_, _, _, err = ssh.NewClientConn(raw, net.JoinHostPort(host, strconv.Itoa(port)), cfg)
	if !errors.Is(err, observed) || fingerprint == "" {
		return "", errors.New("SSH host discovery handshake failed")
	}
	return fingerprint, nil
}
