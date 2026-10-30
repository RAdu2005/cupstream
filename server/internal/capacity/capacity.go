// Package capacity measures the server's outgoing traffic and decides
// whether another viewer fits (overview 9.3). The network, not the CPU, is
// the limit: 1 Gbps carries about 125 viewers of an 8 Mbps stream.
package capacity

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// How often the interfaces' counters are read.
	sampleInterval = time.Second
	// How long an admitted viewer's bitrate counts on top of the measured
	// traffic: until it shows up in the counters, after the keyframe.
	reserveFor = 5 * time.Second
	// The bitrate assumed for a stream that has not reported one yet: 1080p60.
	defaultBitrate = 8_000_000
)

// sysNet is where Linux lists network interfaces. /proc/net is hidden from
// the systemd service (ProcSubset=pid), and netlink is not allowed.
var sysNet = "/sys/class/net"

// Usage is the measured traffic and the limit, in bits per second.
type Usage struct {
	Interfaces []string
	Rate       int64 // measured, averaged over the last second
	Reserved   int64 // admitted viewers not measured yet
	Limit      int64
	// Refusing is set while viewers are being refused.
	Refusing bool
}

// Meter follows the outgoing traffic of some network interfaces.
type Meter struct {
	ifaces []string
	limit  int64
	now    func() time.Time

	mu       sync.Mutex
	txBytes  uint64
	at       time.Time
	rate     int64
	failing  bool // reading the counters failed last time
	reserved []reservation
	refused  time.Time // last refusal
}

type reservation struct {
	bitrate int64
	at      time.Time
}

// New returns a meter for ifaces (all physical interfaces when empty) that
// admits viewers while the outgoing traffic stays under limit bits per
// second.
func New(ifaces []string, limit int64) (*Meter, error) {
	if len(ifaces) == 0 {
		var err error
		if ifaces, err = physicalInterfaces(); err != nil {
			return nil, err
		}
	}
	m := &Meter{ifaces: ifaces, limit: limit, now: time.Now}
	if _, err := m.read(); err != nil {
		return nil, err
	}
	return m, nil
}

// physicalInterfaces lists the interfaces backed by a device, which leaves
// out loopback, bridges, tunnels and containers' virtual interfaces.
func physicalInterfaces() ([]string, error) {
	entries, err := os.ReadDir(sysNet)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if _, err := os.Stat(filepath.Join(sysNet, e.Name(), "device")); err == nil {
			out = append(out, e.Name())
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no physical network interface found; set CUPSTREAM_EGRESS_INTERFACE")
	}
	return out, nil
}

// read returns the bytes sent so far on all of the meter's interfaces.
func (m *Meter) read() (uint64, error) {
	var total uint64
	for _, name := range m.ifaces {
		if name == "" || strings.ContainsAny(name, "/.") {
			return 0, fmt.Errorf("%q is not a network interface name", name)
		}
		b, err := os.ReadFile(filepath.Join(sysNet, name, "statistics", "tx_bytes"))
		if err != nil {
			return 0, fmt.Errorf("reading interface %s: %w", name, err)
		}
		n, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("interface %s: %w", name, err)
		}
		total += n
	}
	return total, nil
}

// Run measures the traffic every second until ctx is done.
func (m *Meter) Run(ctx context.Context) {
	t := time.NewTicker(sampleInterval)
	defer t.Stop()
	m.sample()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.sample()
		}
	}
}

func (m *Meter) sample() {
	n, err := m.read()
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		// Keep the last rate: an interface that vanished is not a reason
		// to admit everyone.
		if !m.failing {
			slog.Warn("measuring outgoing traffic failed", "err", err)
		}
		m.failing = true
		return
	}
	m.failing = false
	if d := now.Sub(m.at); !m.at.IsZero() && d > 0 && n >= m.txBytes {
		m.rate = int64(float64(n-m.txBytes) * 8 / d.Seconds())
	}
	m.txBytes, m.at = n, now
}

// reservedLocked drops reservations that the counters show by now and
// returns the sum of the others.
func (m *Meter) reservedLocked(now time.Time) int64 {
	var sum int64
	kept := m.reserved[:0]
	for _, r := range m.reserved {
		if now.Sub(r.at) < reserveFor {
			kept = append(kept, r)
			sum += r.bitrate
		}
	}
	m.reserved = kept
	return sum
}

// Admit reports whether one more viewer of a stream at bitrate bits per
// second (0 when unknown) fits under the limit, and counts them if so.
func (m *Meter) Admit(bitrate int64) bool {
	if bitrate <= 0 {
		bitrate = defaultBitrate
	}
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rate+m.reservedLocked(now)+bitrate > m.limit {
		m.refused = now
		return false
	}
	m.reserved = append(m.reserved, reservation{bitrate, now})
	return true
}

// Usage returns the current measurement.
func (m *Meter) Usage() Usage {
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	return Usage{
		Interfaces: m.ifaces,
		Rate:       m.rate,
		Reserved:   m.reservedLocked(now),
		Limit:      m.limit,
		Refusing:   !m.refused.IsZero() && now.Sub(m.refused) < reserveFor,
	}
}
