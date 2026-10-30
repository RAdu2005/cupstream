package httpapi

import (
	"net/http"
	"testing"
	"time"

	"github.com/RAdu2005/cupstream/server/internal/api"
)

func TestBandwidthTest(t *testing.T) {
	b := newTestEnv(t, envOptions{}).browser(t)
	r := b.do("GET", "/api/bandwidth-test?bytes=1000000", "")
	if r.status != 200 || len(r.body) != 1000000 || r.header.Get("Cache-Control") != "no-store" || r.header.Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("got %d, %d bytes, %v", r.status, len(r.body), r.header)
	}
	if r := b.do("GET", "/api/bandwidth-test", ""); len(r.body) != defaultTestBytes {
		t.Fatalf("default size %d", len(r.body))
	}
	for _, q := range []string{"0", "-1", "x", "33554433"} {
		if r := b.do("GET", "/api/bandwidth-test?bytes="+q, ""); r.status != http.StatusBadRequest {
			t.Fatalf("bytes=%s: %d", q, r.status)
		}
	}
}

func TestViewerReports(t *testing.T) {
	e := newTestEnv(t, envOptions{devLogin: true})
	alice, bob := e.browser(t), e.browser(t)
	alice.devLogin("alice")
	bob.devLogin("bob")
	slug := alice.roomSlug()
	alice.json("PATCH", "/api/rooms/"+slug, api.UpdateRoomRequest{Visibility: "signed_in"}, nil, 200)
	if r := alice.obs(alice.streamKey(), "", h264Offer); r.status != http.StatusCreated {
		t.Fatalf("OBS = %d %s", r.status, r.body)
	}
	e.reconcile(t)
	streamID := bob.room(slug).Streams[0].ID
	if r := bob.do("POST", "/api/streams/"+streamID+"/whep?tab=bob-tab-1", h264Offer, "Content-Type", "application/sdp"); r.status != http.StatusCreated {
		t.Fatalf("WHEP = %d %s", r.status, r.body)
	}

	ws := bob.connect("bob-tab-1")
	ws.subscribe(slug)
	report := func(room string, r *api.ViewerReport) {
		ws.send(api.ClientMessage{Type: "viewerReport", Room: room, Report: r})
	}
	report(slug, nil)
	if e := next[api.RealtimeError](ws, "error"); e.Code != "invalid" {
		t.Fatalf("error = %+v", e)
	}
	report(slug, &api.ViewerReport{Limited: true, CapacityBps: 20e6, Streams: []api.StreamReport{
		{StreamID: streamID, BitrateBps: 3e6, LossRatio: 7, Freezes: 2, FramesPerSecond: 29.5},
		{StreamID: "aaaaaaaaaaaaaaaa", BitrateBps: 1},
	}})

	var got []any
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		reps := e.gw.ViewerReports(streamID)
		if len(reps) == 1 {
			r := reps[0]
			if !r.Limited || r.CapacityBps != 20e6 || r.Stream.BitrateBps != 3e6 || r.Stream.LossRatio != 1 || r.Stream.Freezes != 2 {
				t.Fatalf("report = %+v", r)
			}
			return
		}
		got = append(got, reps)
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no report recorded: %v", got)
}

func TestViewerReportsNeedTheTabsSession(t *testing.T) {
	e := newTestEnv(t, envOptions{devLogin: true})
	alice, bob := e.browser(t), e.browser(t)
	alice.devLogin("alice")
	bob.devLogin("bob")
	slug := alice.roomSlug()
	alice.json("PATCH", "/api/rooms/"+slug, api.UpdateRoomRequest{Visibility: "signed_in"}, nil, 200)
	alice.obs(alice.streamKey(), "", h264Offer)
	e.reconcile(t)
	streamID := bob.room(slug).Streams[0].ID
	// Bob plays in another tab; this tab's reports are about nothing it plays.
	bob.do("POST", "/api/streams/"+streamID+"/whep?tab=bob-tab-2", h264Offer, "Content-Type", "application/sdp")
	ws := bob.connect("bob-tab-1")
	ws.subscribe(slug)
	ws.send(api.ClientMessage{Type: "viewerReport", Room: slug, Report: &api.ViewerReport{Streams: []api.StreamReport{{StreamID: streamID, BitrateBps: 1}}}})
	// A message after it, answered, shows the report was handled.
	ws.send(api.ClientMessage{Type: "chat", Room: slug, Body: "hi"})
	next[api.ChatEvent](ws, "chat")
	if reps := e.gw.ViewerReports(streamID); len(reps) != 0 {
		t.Fatalf("reports = %+v", reps)
	}
}
