// Command loadtest finds where a development CupStream server saturates
// (overview, section 10): it publishes synthetic streams over WHIP, opens
// WHEP viewers at a steady pace, and reports every second what the viewers
// receive, what MediaMTX sends and how much CPU it takes.
//
// It only talks to servers on this machine. Never point it at production.
//
//	DEV_WEB_PORT=5177 DEV_SERVER_PORT=8084 CUPSTREAM_RATE_LIMITS=false make dev
//	cd server/loadtest && go run . -server http://127.0.0.1:8084 -viewers 100
//
// See README.md next to this file.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"
)

type options struct {
	server      *url.URL
	mediamtxAPI string
	mediamtxPID int
	serverPID   int

	streams   int
	streamIDs []string
	bitrate   int // bits per second
	width     int
	height    int
	fps       int

	viewers int
	ramp    float64 // viewers per second
	hold    time.Duration
	paused  bool // open viewers paused, then resume them
	iface   string
	rcvbuf  int
}

func main() {
	log.SetFlags(0)
	var o options
	server := flag.String("server", "http://127.0.0.1:8084", "the CupStream server (Go, not Vite); must be on this machine")
	flag.StringVar(&o.mediamtxAPI, "mediamtx-api", "http://127.0.0.1:10001", "MediaMTX's API, for what it sends (empty: skip)")
	flag.IntVar(&o.mediamtxPID, "mediamtx-pid", 0, "MediaMTX's process ID, for its CPU use")
	flag.IntVar(&o.serverPID, "server-pid", 0, "the CupStream server's process ID, for its CPU use")
	flag.IntVar(&o.streams, "streams", 1, "synthetic streams to publish")
	ids := flag.String("watch", "", "comma-separated IDs of live streams to watch instead of publishing")
	kbps := flag.Int("bitrate", 8000, "bitrate of each synthetic stream, kbit/s")
	size := flag.String("size", "1920x1080", "picture size of the synthetic streams")
	flag.IntVar(&o.fps, "fps", 60, "frame rate of the synthetic streams")
	flag.IntVar(&o.viewers, "viewers", 50, "viewers to open, spread over the streams")
	flag.Float64Var(&o.ramp, "ramp", 5, "viewers opened per second")
	flag.DurationVar(&o.hold, "hold", 20*time.Second, "how long to keep all viewers after the ramp")
	flag.BoolVar(&o.paused, "prewarm", false, "open viewers paused (pre-warmed), then resume them")
	flag.StringVar(&o.iface, "iface", "", "network interface for the harness's ICE candidates (empty: all)")
	flag.IntVar(&o.rcvbuf, "rcvbuf", 4<<20, "receive buffer of each viewer's UDP socket, in bytes (capped by net.core.rmem_max)")
	flag.Parse()

	var err error
	if o.server, err = url.Parse(*server); err != nil || !isLocal(o.server.Hostname()) {
		log.Fatalf("-server %q: only servers on this machine (localhost, 127.0.0.1, ::1) may be load-tested", *server)
	}
	if o.mediamtxAPI != "" {
		if u, err := url.Parse(o.mediamtxAPI); err != nil || !isLocal(u.Hostname()) {
			log.Fatalf("-mediamtx-api %q must be on this machine", o.mediamtxAPI)
		}
	}
	if _, err := fmt.Sscanf(*size, "%dx%d", &o.width, &o.height); err != nil {
		log.Fatalf("-size %q is not WIDTHxHEIGHT", *size)
	}
	o.bitrate = *kbps * 1000
	for id := range strings.SplitSeq(*ids, ",") {
		if id = strings.TrimSpace(id); id != "" {
			o.streamIDs = append(o.streamIDs, id)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, o); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatal(err)
	}
}

func isLocal(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// client is a browser-like HTTP client with its own cookies.
func newClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, Timeout: 15 * time.Second}
}

func run(ctx context.Context, o options) error {
	streams := o.streamIDs
	var pubs []*publisher
	if len(streams) == 0 {
		clip, err := encodeClip(ctx, o)
		if err != nil {
			return err
		}
		log.Printf("clip: %d frames, %.1f Mbit/s", len(clip.frames), float64(clip.bitrate())/1e6)
		for i := range o.streams {
			p, err := publish(ctx, o, clip, fmt.Sprintf("loadtest_pub_%d", i+1))
			if err != nil {
				return fmt.Errorf("publisher %d: %w", i+1, err)
			}
			defer p.close()
			pubs = append(pubs, p)
			streams = append(streams, p.streamID)
		}
		log.Printf("publishing %d streams; waiting for them to go live", len(pubs))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}

	rep := newReporter(o)
	var wg sync.WaitGroup
	defer func() {
		log.Printf("closing %d viewers", rep.count())
		rep.closeAll()
		wg.Wait()
		rep.summary()
	}()

	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	interval := time.Duration(float64(time.Second) / o.ramp)
	open := time.NewTicker(interval)
	defer open.Stop()
	start := time.Now()
	var opened int
	var holdUntil time.Time
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-open.C:
			if opened >= o.viewers {
				open.Stop()
				if holdUntil.IsZero() {
					holdUntil = time.Now().Add(o.hold)
				}
				continue
			}
			id := streams[opened%len(streams)]
			opened++
			wg.Add(1)
			go func() {
				defer wg.Done()
				rep.add(watch(ctx, o, id))
			}()
		case <-tick.C:
			rep.line(time.Since(start))
			if !holdUntil.IsZero() && time.Now().After(holdUntil) {
				return nil
			}
		}
	}
}
