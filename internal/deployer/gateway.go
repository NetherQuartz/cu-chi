package deployer

import (
	"archive/tar"
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"strings"
)

var GatewayImage string
var gatewayImageRE = regexp.MustCompile(`^ghcr\.io/netherquartz/cu-chi-gateway@sha256:[a-f0-9]{64}$`)

func ValidGatewayImage(image string) bool { return gatewayImageRE.MatchString(image) }

//go:embed gateway.sh
var gatewayApply string

type GatewayPayload struct {
	ID, Image, LANIP, RouterIP, Uplink, LAN string
	Generation                              int64
}

func (SSH) CheckGateway(ctx context.Context, c Credentials) error { return (SSH{}).Check(ctx, c) }
func (SSH) ApplyGateway(ctx context.Context, c Credentials, p GatewayPayload) error {
	lan, e := netip.ParseAddr(p.LANIP)
	router, re := netip.ParseAddr(p.RouterIP)
	if !idRE.MatchString(p.ID) || !ValidGatewayImage(p.Image) || p.Generation < 1 || e != nil || re != nil || !lan.Is4() || !lan.IsPrivate() || !router.Is4() || !router.IsPrivate() {
		return errors.New("invalid gateway deployment")
	}
	client, err := dial(ctx, c)
	if err != nil {
		return err
	}
	defer client.Close()
	out, err := run(ctx, client, c.User, preflight, nil, nil)
	if err != nil {
		return err
	}
	if !strings.Contains(out, "CUCHI_PREFLIGHT_OK") {
		return errors.New("preflight response missing")
	}
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	for _, f := range []struct{ name, data string }{{"uplink.conf", p.Uplink}, {"wg0.conf", p.LAN}, {"router-ip", p.RouterIP}} {
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
	out, err = run(ctx, client, c.User, gatewayApply, []string{p.ID, p.Image, p.LANIP, fmt.Sprint(p.Generation)}, b.Bytes())
	if err == nil && !strings.Contains(out, "CUCHI_GATEWAY_OK") {
		return errors.New("gateway acknowledgement missing")
	}
	return err
}
