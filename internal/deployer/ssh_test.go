package deployer

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"net"
	"os/exec"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestPinnedSSHHandshakeRejectsChangedHost(t *testing.T) {
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(key)
	block, _ := ssh.MarshalPrivateKey(key, "ephemeral test")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 2; i++ {
			raw, err := listener.Accept()
			if err != nil {
				return
			}
			cfg := &ssh.ServerConfig{PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) { return nil, nil }}
			cfg.AddHostKey(signer)
			server, _, _, err := ssh.NewServerConn(raw, cfg)
			if err == nil {
				server.Close()
			}
			raw.Close()
		}
	}()
	c := Credentials{Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port, User: "root", Key: pem.EncodeToMemory(block), Fingerprint: ssh.FingerprintSHA256(signer.PublicKey())}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := dial(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	client.Close()
	c.Fingerprint = "SHA256:wrong"
	if _, err = dial(ctx, c); !errors.Is(err, ErrHostKey) {
		t.Fatalf("changed host accepted: %v", err)
	}
	<-done
}
func TestShellQuotingAndRuntimePin(t *testing.T) {
	value := "$(exit 9) `exit 8` '\n; exit 7"
	result, err := exec.Command("/bin/sh", "-c", "printf %s "+quote(value)).Output()
	if err != nil || string(result) != value {
		t.Fatal("shell argument escaped its boundary")
	}
	digest := "ghcr.io/netherquartz/cu-chi-awg@sha256:" + strings.Repeat("a", 64)
	if !ValidImage(digest) {
		t.Fatal("valid digest rejected")
	}
	for _, bad := range []string{"ghcr.io/netherquartz/cu-chi-awg:main", digest + "; id", "docker.io/other@sha256:" + strings.Repeat("a", 64)} {
		if ValidImage(bad) {
			t.Fatal("unpinned/foreign image accepted")
		}
	}
}
func TestSSHHandshakeCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		raw, err := listener.Accept()
		if err != nil {
			return
		}
		defer raw.Close()
		<-stop
	}()
	defer func() { close(stop); listener.Close(); <-done }()
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	block, _ := ssh.MarshalPrivateKey(key, "")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = dial(ctx, Credentials{Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port, User: "root", Key: pem.EncodeToMemory(block)})
	if err == nil || time.Since(start) > time.Second {
		t.Fatal("handshake did not respect context")
	}
}
