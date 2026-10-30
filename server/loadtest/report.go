package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// reporter prints a line per second and a summary at the end.
type reporter struct {
	o options

	mu      sync.Mutex
	viewers []*viewer

	at       time.Time
	mtxBytes int64
	udpDrops int64
	cpu      map[string]cpuSample

	// For the summary.
	peakPlaying int
	peakRecv    float64
	peakMtxOut  float64
	peakCPU     map[string]float64
	starvedAt   time.Duration // first second with more than 5% starved viewers
	errors      map[string]int
}

func newReporter(o options) *reporter {
	return &reporter{o: o, cpu: map[string]cpuSample{}, peakCPU: map[string]float64{}, errors: map[string]int{}}
}

func (r *reporter) add(v *viewer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.viewers = append(r.viewers, v)
	if v.err != nil {
		msg := v.err.Error()
		if len(msg) > 120 {
			msg = msg[:120]
		}
		r.errors[msg]++
	}
}

func (r *reporter) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.viewers)
}

func (r *reporter) closeAll() {
	r.mu.Lock()
	vs := slices.Clone(r.viewers)
	r.mu.Unlock()
	closeAll(vs)
}

func (r *reporter) line(elapsed time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	secs := now.Sub(r.at).Seconds()
	first := r.at.IsZero()
	r.at = now

	var playing, starting, refused, limited, failed, starved int
	var recv, lost, pkts int64
	for _, v := range r.viewers {
		b, l, p := v.bytes.Load(), v.lost.Load(), v.packets.Load()
		db, dl, dp := b-v.lastBytes, l-v.lastLost, p-v.lastPkts
		v.lastBytes, v.lastLost, v.lastPkts = b, l, p
		switch {
		case v.refused():
			refused++
		case v.rateLimited():
			limited++
		case v.err != nil:
			failed++
		case v.firstKey.Load() == 0:
			starting++
		default:
			playing++
			recv += db
			lost += dl
			pkts += dp
			// Receiving under 80% of the stream's bitrate, a while after
			// starting.
			settled := now.Sub(time.Unix(0, v.firstKey.Load())) > 2*time.Second
			if !first && settled && r.o.bitrate > 0 && float64(db*8)/secs < 0.8*float64(r.o.bitrate) {
				starved++
			}
		}
	}
	if first {
		return
	}
	recvMbps := float64(recv*8) / secs / 1e6
	lossPct := 0.0
	if pkts+lost > 0 {
		lossPct = 100 * float64(lost) / float64(pkts+lost)
	}
	r.peakPlaying = max(r.peakPlaying, playing)
	r.peakRecv = max(r.peakRecv, recvMbps)
	if r.starvedAt == 0 && playing > 0 && starved*20 > playing {
		r.starvedAt = elapsed
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%4ds  playing %3d  starting %2d  refused %2d  limited %2d  failed %2d | recv %7.1f Mbit/s  loss %5.2f%%  starved %3d",
		int(elapsed.Seconds()), playing, starting, refused, limited, failed, recvMbps, lossPct, starved)
	if n, err := udpReceiveDrops(); err == nil {
		if r.udpDrops > 0 || n == 0 {
			fmt.Fprintf(&b, "  udp drops %5d", n-r.udpDrops)
		}
		r.udpDrops = n
	}
	if r.o.mediamtxAPI != "" {
		if n, err := mediamtxSent(r.o.mediamtxAPI); err == nil {
			if r.mtxBytes > 0 {
				out := float64((n-r.mtxBytes)*8) / secs / 1e6
				r.peakMtxOut = max(r.peakMtxOut, out)
				fmt.Fprintf(&b, " | mediamtx out %7.1f Mbit/s", out)
			}
			r.mtxBytes = n
		}
	}
	for _, p := range []struct {
		name string
		pid  int
	}{{"mediamtx", r.o.mediamtxPID}, {"server", r.o.serverPID}, {"harness", os.Getpid()}} {
		if p.pid == 0 {
			continue
		}
		if pct, ok := r.cpuPercent(p.name, p.pid, now); ok {
			r.peakCPU[p.name] = max(r.peakCPU[p.name], pct)
			fmt.Fprintf(&b, "  %s cpu %4.0f%%", p.name, pct)
		}
	}
	log.Print(b.String())
}

func (r *reporter) summary() {
	r.mu.Lock()
	defer r.mu.Unlock()
	var keyframe []time.Duration
	for _, v := range r.viewers {
		if k := v.firstKey.Load(); k != 0 {
			keyframe = append(keyframe, time.Unix(0, k).Sub(v.opened))
		}
	}
	slices.Sort(keyframe)
	pct := func(p float64) time.Duration {
		if len(keyframe) == 0 {
			return 0
		}
		return keyframe[min(len(keyframe)-1, int(p*float64(len(keyframe))))].Round(time.Millisecond)
	}

	log.Printf("summary: %d viewers opened, at most %d playing at once", len(r.viewers), r.peakPlaying)
	log.Printf("  received at most %.1f Mbit/s; MediaMTX sent at most %.1f Mbit/s", r.peakRecv, r.peakMtxOut)
	if len(keyframe) > 0 {
		log.Printf("  first keyframe after opening (or resuming): median %v, 95th percentile %v, worst %v", pct(0.5), pct(0.95), keyframe[len(keyframe)-1].Round(time.Millisecond))
	}
	if r.starvedAt > 0 {
		log.Printf("  more than 5%% of viewers got under 80%% of the bitrate from %ds on", int(r.starvedAt.Seconds()))
	}
	for name, p := range r.peakCPU {
		log.Printf("  peak CPU of %s: %.0f%% of one core", name, p)
	}
	for msg, n := range r.errors {
		log.Printf("  %d× %s", n, msg)
	}
}

// mediamtxSent returns the bytes MediaMTX has sent to its WebRTC readers.
func mediamtxSent(api string) (int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(api, "/")+"/v3/webrtc/sessions/list?itemsPerPage=100000", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	var list struct {
		Items []struct {
			State         string `json:"state"`
			OutboundBytes int64  `json:"outboundBytes"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return 0, err
	}
	var n int64
	for _, s := range list.Items {
		if s.State == "read" {
			n += s.OutboundBytes
		}
	}
	return n, nil
}

type cpuSample struct {
	ticks int64
	at    time.Time
}

// cpuPercent returns the CPU a process used since the last call, in percent
// of one core, from /proc/<pid>/stat.
func (r *reporter) cpuPercent(name string, pid int, now time.Time) (float64, bool) {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, false
	}
	// The command name may contain spaces; the fields after it don't.
	s := string(raw)
	fields := strings.Fields(s[strings.LastIndexByte(s, ')')+1:])
	if len(fields) < 13 {
		return 0, false
	}
	utime, _ := strconv.ParseInt(fields[11], 10, 64)
	stime, _ := strconv.ParseInt(fields[12], 10, 64)
	cur := cpuSample{utime + stime, now}
	prev, ok := r.cpu[name]
	r.cpu[name] = cur
	if !ok {
		return 0, false
	}
	const ticksPerSecond = 100 // USER_HZ on Linux
	return 100 * float64(cur.ticks-prev.ticks) / ticksPerSecond / now.Sub(prev.at).Seconds(), true
}

// udpReceiveDrops returns how many UDP datagrams this machine dropped
// because a socket's receive buffer was full (RcvbufErrors).
func udpReceiveDrops() (int64, error) {
	raw, err := os.ReadFile("/proc/net/snmp")
	if err != nil {
		return 0, err
	}
	var names []string
	for line := range strings.SplitSeq(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "Udp:" {
			continue
		}
		if names == nil {
			names = fields
			continue
		}
		for i, name := range names {
			if name == "RcvbufErrors" && i < len(fields) {
				return strconv.ParseInt(fields[i], 10, 64)
			}
		}
	}
	return 0, fmt.Errorf("no RcvbufErrors in /proc/net/snmp")
}
