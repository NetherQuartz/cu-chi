package telemetry

import "time"

// Samples contain counters and public peer identities only, never configuration or keys.
type Peer struct {
	Key               string
	RX, TX, Handshake int64
}
type Sample struct {
	At          time.Time `json:"at"`
	Boot        string    `json:"boot"`
	Epoch       string    `json:"epoch"`
	Interface   string    `json:"interface"`
	CPU, Idle   int64
	Cores       int     `json:"cores"`
	MemoryTotal int64   `json:"memory_total"`
	MemoryUsed  int64   `json:"memory_used"`
	DiskTotal   int64   `json:"disk_total"`
	DiskUsed    int64   `json:"disk_used"`
	RX          int64   `json:"rx"`
	TX          int64   `json:"tx"`
	Uptime      float64 `json:"uptime"`
	Tunnel      bool    `json:"tunnel"`
	Peers       []Peer  `json:"-"`
}
type Point struct {
	Sample
	CPUPercent  *float64 `json:"cpu_percent"`
	ReceiveRate *float64 `json:"receive_rate"`
	SendRate    *float64 `json:"send_rate"`
}

func Between(s Sample, old *Sample) Point {
	p := Point{Sample: s}
	p.Peers = nil
	if old == nil || old.Boot != s.Boot {
		return p
	}
	dt := s.At.Sub(old.At).Seconds()
	if dt < 1 || dt > 180 {
		return p
	}
	if s.CPU > old.CPU && s.Idle >= old.Idle && s.Idle-old.Idle <= s.CPU-old.CPU {
		v := 100 * (1 - float64(s.Idle-old.Idle)/float64(s.CPU-old.CPU))
		p.CPUPercent = &v
	}
	if old.Interface == s.Interface && s.RX >= old.RX && s.TX >= old.TX {
		rx := float64(s.RX-old.RX) / dt
		tx := float64(s.TX-old.TX) / dt
		p.ReceiveRate = &rx
		p.SendRate = &tx
	}
	return p
}

// A reset starts a fresh baseline: never turn an unknown interval into a spike.
func Delta(rx, tx, oldRX, oldTX int64, sameEpoch bool) (int64, int64, bool) {
	if !sameEpoch || rx < oldRX || tx < oldTX {
		return 0, 0, true
	}
	return rx - oldRX, tx - oldTX, false
}
