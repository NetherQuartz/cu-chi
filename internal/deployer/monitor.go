package deployer

import (
	"context"
	_ "embed"
	"encoding/base64"
	"errors"
	"github.com/NetherQuartz/cu-chi/internal/telemetry"
	"math"
	"strconv"
	"strings"
	"time"
)

//go:embed monitor.sh
var monitorScript string

func (SSH) Observe(ctx context.Context, c Credentials, id string) (telemetry.Sample, error) {
	if !idRE.MatchString(id) {
		return telemetry.Sample{}, errors.New("invalid server identity")
	}
	client, err := dial(ctx, c)
	if err != nil {
		return telemetry.Sample{}, err
	}
	defer client.Close()
	out, err := run(ctx, client, c.User, monitorScript, []string{id}, nil)
	if err != nil {
		return telemetry.Sample{}, err
	}
	return parseObservation(out, time.Now().UTC())
}
func parseObservation(out string, at time.Time) (s telemetry.Sample, err error) {
	bad := errors.New("invalid monitoring response")
	s.At = at
	seen := map[string]bool{}
	peers := map[string]*telemetry.Peer{}
	hands := map[string]int64{}
	num := func(x string) (int64, error) {
		v, e := strconv.ParseInt(x, 10, 64)
		if e != nil || v < 0 {
			return 0, bad
		}
		return v, nil
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			return s, bad
		}
		kind := f[0]
		if kind != "PEER" && kind != "HANDSHAKE" {
			if seen[kind] {
				return s, bad
			}
			seen[kind] = true
		}
		switch kind {
		case "BOOT", "EPOCH":
			if len(f) != 2 || len(f[1]) > 80 {
				return s, bad
			}
			if kind == "BOOT" {
				s.Boot = f[1]
			} else {
				s.Epoch = f[1]
			}
		case "CPU", "MEM", "DISK":
			if len(f) != 3 {
				return s, bad
			}
			a, e := num(f[1])
			b, e2 := num(f[2])
			if e != nil || e2 != nil || b > a || a == 0 {
				return s, bad
			}
			switch kind {
			case "CPU":
				s.CPU = a
				s.Idle = b
			case "MEM":
				s.MemoryTotal = a
				s.MemoryUsed = b
			case "DISK":
				s.DiskTotal = a
				s.DiskUsed = b
			}
		case "CORES":
			if len(f) != 2 {
				return s, bad
			}
			n, e := num(f[1])
			if e != nil || n < 1 || n > 65536 {
				return s, bad
			}
			s.Cores = int(n)
		case "UPTIME":
			if len(f) != 2 {
				return s, bad
			}
			v, e := strconv.ParseFloat(f[1], 64)
			if e != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1e12 {
				return s, bad
			}
			s.Uptime = v
		case "NET":
			if len(f) != 4 || len(f[1]) > 64 {
				return s, bad
			}
			s.Interface = f[1]
			s.RX, err = num(f[2])
			if err != nil {
				return s, bad
			}
			s.TX, err = num(f[3])
			if err != nil {
				return s, bad
			}
		case "TUNNEL":
			if len(f) != 2 || (f[1] != "0" && f[1] != "1") {
				return s, bad
			}
			s.Tunnel = f[1] == "1"
		case "PEER", "HANDSHAKE":
			want := 4
			if kind == "HANDSHAKE" {
				want = 3
			}
			if len(f) != want {
				return s, bad
			}
			key, e := base64.StdEncoding.DecodeString(f[1])
			if e != nil || len(key) != 32 {
				return s, bad
			}
			a, e := num(f[2])
			if e != nil {
				return s, bad
			}
			if kind == "PEER" {
				if peers[f[1]] != nil {
					return s, bad
				}
				b, e := num(f[3])
				if e != nil {
					return s, bad
				}
				peers[f[1]] = &telemetry.Peer{Key: f[1], RX: a, TX: b}
			} else {
				if _, ok := hands[f[1]]; ok {
					return s, bad
				}
				if a > at.Add(5*time.Minute).Unix() {
					return s, bad
				}
				hands[f[1]] = a
			}
		case "END":
			if len(f) != 1 {
				return s, bad
			}
		default:
			return s, bad
		}
	}
	for _, k := range []string{"BOOT", "CPU", "CORES", "MEM", "DISK", "UPTIME", "NET", "TUNNEL", "END"} {
		if !seen[k] {
			return s, bad
		}
	}
	if s.Tunnel && !seen["EPOCH"] {
		return s, bad
	}
	if !s.Tunnel && (len(peers) > 0 || len(hands) > 0) {
		return s, bad
	}
	for key, p := range peers {
		h, ok := hands[key]
		if !ok {
			return s, bad
		}
		p.Handshake = h
		s.Peers = append(s.Peers, *p)
	}
	if len(peers) != len(hands) {
		return s, bad
	}
	return s, nil
}
