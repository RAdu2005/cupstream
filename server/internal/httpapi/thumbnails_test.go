package httpapi

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/RAdu2005/cupstream/server/internal/api"
)

func encodeJPEG(t *testing.T, w, h int) string {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h)), nil); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestThumbnails(t *testing.T) {
	e := newTestEnv(t, envOptions{devLogin: true})
	alice, carol, dave, anon := e.browser(t), e.browser(t), e.browser(t), e.browser(t)
	alice.devLogin("alice")
	carol.devLogin("carol")
	dave.devLogin("dave")
	slug := alice.roomSlug()
	alice.json("PUT", "/api/rooms/"+slug+"/allowed/carol", api.SetAllowedRequest{}, nil, 200)

	var started api.StartStreamResponse
	alice.json("POST", "/api/streams", api.StartStreamRequest{Source: "web", LinkFallback: true}, &started, http.StatusCreated)
	thumb := "/api/streams/" + started.Stream.ID + "/thumbnail"
	put := func(body, contentType, token string) response {
		return alice.do("PUT", thumb, body, "Content-Type", contentType, "Authorization", "Bearer "+token)
	}
	if r := carol.do("GET", thumb, ""); r.status != http.StatusNotFound {
		t.Fatalf("GET before an upload = %d %s", r.status, r.body)
	}

	// The publish token is required, and only small JPEGs are accepted.
	pic := encodeJPEG(t, 320, 180)
	var pngBuf bytes.Buffer
	png.Encode(&pngBuf, image.NewRGBA(image.Rect(0, 0, 4, 4)))
	for name, tc := range map[string]struct {
		body, contentType, token string
		status                   int
	}{
		"no token":        {pic, "image/jpeg", "", http.StatusUnauthorized},
		"wrong token":     {pic, "image/jpeg", "nope", http.StatusUnauthorized},
		"not JPEG":        {pngBuf.String(), "image/png", started.Token, http.StatusUnsupportedMediaType},
		"PNG called JPEG": {pngBuf.String(), "image/jpeg", started.Token, http.StatusUnprocessableEntity},
		"too wide":        {encodeJPEG(t, 1281, 10), "image/jpeg", started.Token, http.StatusUnprocessableEntity},
		"too large":       {strings.Repeat("x", 256<<10+1), "image/jpeg", started.Token, http.StatusRequestEntityTooLarge},
	} {
		if r := put(tc.body, tc.contentType, tc.token); r.status != tc.status {
			t.Errorf("%s: PUT = %d %s, want %d", name, r.status, r.body, tc.status)
		}
	}

	if r := put(pic, "image/jpeg", started.Token); r.status != http.StatusNoContent {
		t.Fatalf("PUT = %d %s", r.status, r.body)
	}
	// One a second at most.
	if r := put(pic, "image/jpeg", started.Token); r.status != http.StatusTooManyRequests || r.header.Get("Retry-After") == "" {
		t.Fatalf("second PUT = %d %s", r.status, r.body)
	}

	// Viewers who may watch get it; others are refused.
	r := carol.do("GET", thumb, "")
	if r.status != http.StatusOK || r.body != pic || r.header.Get("Content-Type") != "image/jpeg" ||
		r.header.Get("Cache-Control") != "private, no-cache" || r.header.Get("ETag") == "" {
		t.Fatalf("carol GET = %d %v", r.status, r.header)
	}
	if r := carol.do("GET", thumb, "", "If-None-Match", r.header.Get("ETag")); r.status != http.StatusNotModified || r.body != "" {
		t.Fatalf("revalidation = %d", r.status)
	}
	if r := dave.do("GET", thumb, ""); r.status != http.StatusForbidden || !strings.Contains(r.body, "not_allowed") {
		t.Fatalf("dave GET = %d %s", r.status, r.body)
	}
	if r := anon.do("GET", thumb, ""); r.status != http.StatusUnauthorized {
		t.Fatalf("anonymous GET = %d", r.status)
	}

	// The room says when it arrived, which viewers use to refresh it.
	at := carol.room(slug).Streams[0].ThumbnailAt
	if at == 0 || time.Since(time.UnixMilli(at)) > time.Minute {
		t.Fatalf("thumbnailAt = %d", at)
	}

	// It goes with the stream.
	alice.json("DELETE", "/api/streams/"+started.Stream.ID, nil, nil, http.StatusNoContent)
	if r := carol.do("GET", thumb, ""); r.status != http.StatusNotFound {
		t.Fatalf("GET after the stream ended = %d", r.status)
	}
	if r := put(pic, "image/jpeg", started.Token); r.status != http.StatusNotFound {
		t.Fatalf("PUT after the stream ended = %d", r.status)
	}
}

// OBS streams have no publish token, so their thumbnails cannot be set.
func TestThumbnailOBS(t *testing.T) {
	e := newTestEnv(t, envOptions{devLogin: true})
	alice := e.browser(t)
	alice.devLogin("alice")
	key := alice.streamKey()
	alice.obs(key, "", h264Offer)
	e.reconcile(t)
	id := alice.room(alice.roomSlug()).Streams[0].ID
	for _, token := range []string{"", key} {
		r := alice.do("PUT", "/api/streams/"+id+"/thumbnail", encodeJPEG(t, 16, 16), "Content-Type", "image/jpeg", "Authorization", "Bearer "+token)
		if r.status != http.StatusUnauthorized {
			t.Fatalf("PUT with %q = %d", token, r.status)
		}
	}
}
