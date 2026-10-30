package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/RAdu2005/cupstream/server/internal/api"
	"github.com/RAdu2005/cupstream/server/internal/mediamtx/mtxtest"
)

// TestBitrateTargetMessage: the streamer of a web stream gets the target
// the server computed from its viewers; viewers do not.
func TestBitrateTargetMessage(t *testing.T) {
	e := newTestEnv(t, envOptions{devLogin: true})
	alice, bob := e.browser(t), e.browser(t)
	alice.devLogin("alice")
	bob.devLogin("bob")
	slug := alice.roomSlug()
	alice.json("PUT", "/api/rooms/"+slug+"/allowed/bob", api.SetAllowedRequest{}, nil, 200)

	var started api.StartStreamResponse
	alice.json("POST", "/api/streams", api.StartStreamRequest{Source: "web", LinkFallback: true}, &started, http.StatusCreated)
	if r := alice.do("POST", started.WhipURL, h264Offer, "Content-Type", "application/sdp", "Authorization", "Bearer "+started.Token); r.status != http.StatusCreated {
		t.Fatalf("WHIP = %d %s", r.status, r.body)
	}
	if r := bob.whep(started.Stream.ID); r.status != http.StatusCreated {
		t.Fatalf("WHEP = %d %s", r.status, r.body)
	}
	e.reconcile(t)

	// Bob loses a fifth of his packets while Alice sends.
	for _, s := range e.mtx.Sessions("s/" + started.Stream.ID) {
		if s.State == "publish" {
			e.mtx.Update(s.ID, func(s *mtxtest.Session) { s.InboundBytes = 1_000_000 })
		} else {
			e.mtx.Update(s.ID, func(s *mtxtest.Session) { s.OutboundLossRatio = 0.2 })
		}
	}
	time.Sleep(50 * time.Millisecond) // a measurable interval
	e.reconcile(t)

	as := alice.connect("alice-tab")
	as.subscribe(slug)
	tg := next[api.BitrateTarget](as, "bitrateTarget")
	if tg.StreamID != started.Stream.ID || tg.Room != slug || tg.MaxBitrate < 1_000_000 || tg.Viewers != 1 || tg.Congested != 1 {
		t.Fatalf("target = %+v", tg)
	}

	// Bob, in the same room, gets other messages but no target.
	bs := bob.connect("bob-tab-1")
	bs.subscribe(slug)
	ctx, cancel := context.WithTimeout(t.Context(), 2500*time.Millisecond)
	defer cancel()
	for {
		_, data, err := bs.ws.Read(ctx)
		if err != nil {
			break
		}
		var head struct{ Type string }
		json.Unmarshal(data, &head)
		if head.Type == "bitrateTarget" || head.Type == "viewerStats" {
			t.Fatalf("viewer got %s", data)
		}
	}
}
