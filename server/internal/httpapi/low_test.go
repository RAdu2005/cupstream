package httpapi

import (
	"net/http"
	"strings"
	"testing"

	"github.com/RAdu2005/cupstream/server/internal/api"
)

// TestLowQualityRoutes follows a sender's low-quality version through the
// public routes: the start response offers it, the sender publishes it with
// the stream's token, viewers play, pause and close it at its own session
// URLs, and it ends without ending the stream.
func TestLowQualityRoutes(t *testing.T) {
	e := newTestEnv(t, envOptions{devLogin: true, lowQuality: true})
	e.gw.DetectFeatures(t.Context())
	alice, anon := e.browser(t), e.browser(t)
	alice.devLogin("alice")
	slug := alice.roomSlug()
	alice.json("PATCH", "/api/rooms/"+slug, api.UpdateRoomRequest{Visibility: "everyone"}, nil, 200)

	var started api.StartStreamResponse
	alice.json("POST", "/api/streams", api.StartStreamRequest{Source: "app", LinkFallback: true}, &started, http.StatusCreated)
	if !started.LowQuality.Publish || started.Stream.LowQuality != "none" {
		t.Fatalf("start response: %+v", started)
	}
	publish := func(query string) response {
		return alice.do("POST", started.WhipURL+query, h264Offer, "Content-Type", "application/sdp", "Authorization", "Bearer "+started.Token)
	}
	if r := publish("?quality=low"); r.status != http.StatusConflict || !strings.Contains(r.body, "no_main_session") {
		t.Fatalf("low before main = %d %s", r.status, r.body)
	}
	if r := publish(""); r.status != http.StatusCreated {
		t.Fatalf("main = %d %s", r.status, r.body)
	}
	r := publish("?quality=low")
	if r.status != http.StatusCreated || !strings.Contains(r.header.Get("Location"), "/api/media/"+started.Stream.ID+"/low/whip/") {
		t.Fatalf("low = %d %s %v", r.status, r.body, r.header)
	}
	lowPub := r.header.Get("Location")
	e.reconcile(t)
	if st := alice.room(slug).Streams[0]; st.LowQuality != "sent" {
		t.Fatalf("stream with a low version: %+v", st)
	}

	r = anon.do("POST", "/api/streams/"+started.Stream.ID+"/whep?quality=low", h264Offer, "Content-Type", "application/sdp")
	viewer := r.header.Get("Location")
	if r.status != http.StatusCreated || !strings.Contains(viewer, "/low/whep/") {
		t.Fatalf("low viewer = %d %s", r.status, r.body)
	}
	if r := anon.do("PATCH", viewer, "a=end-of-candidates\r\n", "Content-Type", "application/trickle-ice-sdpfrag"); r.status != http.StatusNoContent {
		t.Fatalf("PATCH = %d %s", r.status, r.body)
	}
	if r := anon.do("POST", viewer+"/pause", ""); r.status != http.StatusNoContent {
		t.Fatalf("pause = %d %s", r.status, r.body)
	}
	if r := anon.do("DELETE", viewer, ""); r.status != http.StatusOK {
		t.Fatalf("DELETE = %d %s", r.status, r.body)
	}

	if r := alice.do("DELETE", lowPub, ""); r.status != http.StatusOK {
		t.Fatalf("DELETE low = %d %s", r.status, r.body)
	}
	if st := alice.room(slug).Streams[0]; st.LowQuality != "none" || st.State != "starting" && st.State != "live" {
		t.Fatalf("after the low version ended: %+v", st)
	}
}

func TestLowQualityOffNotOffered(t *testing.T) {
	e := newTestEnv(t, envOptions{devLogin: true})
	alice := e.browser(t)
	alice.devLogin("alice")
	var started api.StartStreamResponse
	alice.json("POST", "/api/streams", api.StartStreamRequest{Source: "app", LinkFallback: true}, &started, http.StatusCreated)
	if started.LowQuality.Publish {
		t.Fatalf("offered while off: %+v", started.LowQuality)
	}
}

// The room's subscribers learn when a stream gains or loses its
// low-quality version.
func TestLowQualityEvents(t *testing.T) {
	e := newTestEnv(t, envOptions{devLogin: true, lowQuality: true})
	alice := e.browser(t)
	alice.devLogin("alice")
	slug := alice.roomSlug()
	as := alice.connect("alice-tab")
	as.subscribe(slug)

	var started api.StartStreamResponse
	alice.json("POST", "/api/streams", api.StartStreamRequest{Source: "app", LinkFallback: true}, &started, http.StatusCreated)
	publish := func(query string) response {
		return alice.do("POST", started.WhipURL+query, h264Offer, "Content-Type", "application/sdp", "Authorization", "Bearer "+started.Token)
	}
	publish("")
	low := publish("?quality=low").header.Get("Location")
	e.reconcile(t)
	next(as, "stream", func(e api.StreamEvent) bool { return e.Stream.LowQuality == "sent" })
	alice.do("DELETE", low, "")
	next(as, "stream", func(e api.StreamEvent) bool { return e.Stream.LowQuality == "none" })
}
