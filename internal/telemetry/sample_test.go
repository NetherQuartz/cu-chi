package telemetry

import (
	"testing"
	"time"
)

func TestRatesRejectResetsAndGaps(t *testing.T) {
	old := Sample{At: time.Unix(1000, 0), Boot: "boot", Interface: "eth0", CPU: 100, Idle: 40, RX: 1000, TX: 2000}
	s := old
	s.At = s.At.Add(time.Minute)
	s.CPU += 100
	s.Idle += 25
	s.RX += 600
	s.TX += 1200
	p := Between(s, &old)
	if p.CPUPercent == nil || *p.CPUPercent != 75 || *p.ReceiveRate != 10 || *p.SendRate != 20 {
		t.Fatal("wrong interval rates")
	}
	for _, change := range []func(*Sample){func(x *Sample) { x.Boot = "new" }, func(x *Sample) { x.At = x.At.Add(10 * time.Minute) }, func(x *Sample) { x.RX = 0; x.CPU = 0 }, func(x *Sample) { x.Interface = "eth1"; x.Idle = 1 }} {
		v := s
		change(&v)
		p = Between(v, &old)
		if p.ReceiveRate != nil || p.CPUPercent != nil {
			t.Fatal("reset/gap must not produce rates")
		}
	}
	if up, down, gap := Delta(1, 1, 100, 200, true); up != 0 || down != 0 || !gap {
		t.Fatal("counter reset counted")
	}
}
