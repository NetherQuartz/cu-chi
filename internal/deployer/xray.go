package deployer

import (
	"archive/tar"
	"bytes"
	"context"
	_ "embed"
	"errors"
	"github.com/NetherQuartz/cu-chi/internal/xray"
	"strconv"
	"strings"
)

//go:embed xray.sh
var xrayApply string

// Initial target selection runs from the VPS, so DNS, certificate verification,
// TLS 1.3 and negotiated HTTP/2 reflect that server's outbound connectivity.
const targetProbe = `set -eu
for host in www.microsoft.com www.apple.com www.cloudflare.com; do
 result=$(timeout 8 openssl s_client -connect "$host:443" -servername "$host" -verify_hostname "$host" -verify_return_error -tls1_3 -alpn h2 </dev/null 2>/dev/null) || continue
 if grep -q 'ALPN protocol: h2' <<<"$result"; then echo "$host"; fi
done`

func (SSH) ApplyXray(ctx context.Context, c Credentials, id string, revision int64, k xray.Identity, sni string, users []string, probe string) (string, error) {
	if !idRE.MatchString(id) || revision <= 0 {
		return "", errors.New("invalid Xray deployment")
	}
	client, e := dial(ctx, c)
	if e != nil {
		return "", e
	}
	defer client.Close()
	targets := []string{sni}
	if sni == "" {
		out, e := run(ctx, client, c.User, targetProbe, nil, nil)
		if e != nil {
			return "", e
		}
		targets = nil
		for _, s := range strings.Fields(out) {
			switch s {
			case "www.microsoft.com", "www.apple.com", "www.cloudflare.com":
				targets = append(targets, s)
			}
		}
	}
	if len(targets) == 0 {
		return "", errors.New("xray_target_unavailable")
	}
	for _, target := range targets {
		server, cl, e := xray.Configs(k, target, probe, users)
		if e != nil {
			return "", e
		}
		var b bytes.Buffer
		tw := tar.NewWriter(&b)
		for _, file := range []struct {
			name string
			data []byte
		}{{"server.json", server}, {"client.json", cl}} {
			if e = tw.WriteHeader(&tar.Header{Name: file.name, Mode: 0600, Size: int64(len(file.data))}); e != nil {
				return "", e
			}
			if _, e = tw.Write(file.data); e != nil {
				return "", e
			}
		}
		if e = tw.Close(); e != nil {
			return "", e
		}
		out, e := run(ctx, client, c.User, xrayApply, []string{id, strconv.FormatInt(revision, 10), xray.Image}, b.Bytes())
		if e != nil {
			if e.Error() == "xray_probe_failed" && sni == "" {
				continue
			}
			return "", e
		}
		if !strings.HasSuffix(strings.TrimSpace(out), "CUCHI_XRAY_OK") {
			return "", errors.New("Xray deployment acknowledgement missing")
		}
		return target, nil
	}
	return "", errors.New("xray_probe_failed")
}
