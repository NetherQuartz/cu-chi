package deployer

import (
	"context"
	_ "embed"
	"errors"
	"github.com/NetherQuartz/cu-chi/internal/egress"
)

//go:embed warp-register.sh
var warpRegister string

// The profile returned by this operation is a secret, never a loggable job result.
func (SSH) RegisterWarp(ctx context.Context, c Credentials, id, image string) (string, error) {
	if !idRE.MatchString(id) || !ValidImage(image) {
		return "", errors.New("invalid WARP registration configuration")
	}
	client, err := dial(ctx, c)
	if err != nil {
		return "", err
	}
	defer client.Close()
	raw, err := run(ctx, client, c.User, warpRegister, []string{id, image}, nil)
	if err != nil {
		return "", err
	}
	if _, err = egress.Parse(raw); err != nil {
		return "", errors.New("warp_configuration_invalid")
	}
	return raw, nil
}
