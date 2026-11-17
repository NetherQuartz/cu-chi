package deployer

import (
	"archive/tar"
	"bytes"
	"context"
	_ "embed"
	"errors"
	"strconv"
	"strings"

	"github.com/NetherQuartz/cu-chi/internal/awg"
)

//go:embed health.sh
var healthScript string

// Probe touches only a fresh synthetic peer, under the deployment lock. It never
// reconciles user peers, restarts a runtime, or downloads an image.
func (SSH) Probe(ctx context.Context, c Credentials, id string, generation int64, settings awg.Settings) string {
	if !idRE.MatchString(id) || generation < 1 {
		return "configuration"
	}
	client, err := dial(ctx, c)
	if err != nil {
		if errors.Is(err, ErrHostKey) {
			return "host_key_mismatch"
		}
		return "management"
	}
	defer client.Close()
	key := awg.NewKeys()
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	for _, f := range []struct{ name, data string }{{"probe.conf", settings.Probe(key)}, {"psk", key.PSK + "\n"}} {
		if tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0600, Size: int64(len(f.data))}) != nil {
			return "configuration"
		}
		if _, err = tw.Write([]byte(f.data)); err != nil {
			return "configuration"
		}
	}
	if tw.Close() != nil {
		return "configuration"
	}
	defer clear(b.Bytes())
	out, err := run(ctx, client, c.User, healthScript, []string{id, strconv.FormatInt(generation, 10), key.Public}, b.Bytes())
	if err != nil {
		return "management"
	}
	switch strings.TrimSpace(out) {
	case "HEALTH_OK":
		return "ok"
	case "HEALTH_DATA":
		return "data"
	case "HEALTH_BUSY":
		return "skipped"
	default:
		return "management"
	}
}
