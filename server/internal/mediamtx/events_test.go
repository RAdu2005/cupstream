package mediamtx

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestFeaturesOf(t *testing.T) {
	for v, want := range map[string]Features{
		"v1.21.2":                               {},
		"v1.21.2-cupstream.1":                   {},
		"v1.21.2-cupstream.2":                   {Pause: true, Events: true},
		"v1.22.0-cupstream.10":                  {Pause: true, Events: true},
		"v1.21.2-cupstream.2x":                  {},
		"v1.21.2-cupstream.3-2-gcece8829-dirty": {Pause: true, Events: true},
		"":                                      {},
	} {
		if got := FeaturesOf(v); got != want {
			t.Errorf("FeaturesOf(%q) = %+v, want %+v", v, got, want)
		}
	}
}

func TestEvents(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/events" {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, "event: ready\ndata: {}\n\n"+
			": keepalive\n\n"+
			"event: stream.online\ndata: {\"type\":\"stream.online\",\"path\":\"s/abc\"}\n\n"+
			"event: session.created\ndata: {\"type\":\"session.created\",\"path\":\"s/abc\",\n"+
			"data: \"state\":\"read\",\"reader\":{\"type\":\"webRTCSession\",\"id\":\"42\"}}\n\n"+
			"event: overflow\ndata: {}\n\n")
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	c := New(u, u)

	var got []Event
	ready := 0
	err := c.Events(context.Background(), func() { ready++ }, func(e Event) {
		if ready != 1 {
			t.Error("event before ready")
		}
		got = append(got, e)
	})
	if !errors.Is(err, ErrOverflow) {
		t.Fatalf("err = %v, want overflow", err)
	}
	if len(got) != 2 || got[0].Type != EventStreamOnline || got[0].Path != "s/abc" ||
		got[1].Type != EventSessionCreated || got[1].SessionID() != "42" || got[1].State != "read" {
		t.Fatalf("events %+v", got)
	}

	// A build without the event stream.
	u.Path = "/other"
	if err := New(u, u).Events(context.Background(), func() {}, func(Event) {}); err == nil {
		t.Fatal("no error for a missing event stream")
	}
}
