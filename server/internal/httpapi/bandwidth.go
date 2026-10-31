package httpapi

import (
	"crypto/rand"
	"net/http"
	"strconv"
)

// The download test of the bandwidth warnings (overview, 7.2): before a
// viewer opens a second stream, the page measures how fast it downloads
// from the server. Browsers' own estimates cannot be used: Chrome caps them
// at 10 Mbps, and Firefox and Safari have none.
const (
	defaultTestBytes = 4 << 20
	maxTestBytes     = 32 << 20
	// Tests running at once; more get 503, so tests cannot take over the
	// server's uplink.
	maxConcurrentTests = 8
)

// testData is random, so that nothing on the way can compress it.
var testData = func() []byte {
	b := make([]byte, 256<<10)
	rand.Read(b)
	return b
}()

var testSlots = make(chan struct{}, maxConcurrentTests)

func (s *Server) bandwidthRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/bandwidth-test", s.bandwidthTest)
}

// bandwidthTest serves GET /api/bandwidth-test?bytes=N: N random bytes
// (4 MiB by default, at most 32 MiB). The page times them as they arrive
// and stops reading when it has measured enough.
func (s *Server) bandwidthTest(w http.ResponseWriter, r *http.Request) {
	n := defaultTestBytes
	if v := r.URL.Query().Get("bytes"); v != "" {
		var err error
		if n, err = strconv.Atoi(v); err != nil || n < 1 || n > maxTestBytes {
			writeError(w, http.StatusBadRequest, "bytes is between 1 and 33554432")
			return
		}
	}
	select {
	case testSlots <- struct{}{}:
		defer func() { <-testSlots }()
	default:
		w.Header().Set("Retry-After", "2")
		writeError(w, http.StatusServiceUnavailable, "too many connection tests are running; try again in a moment")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Length", strconv.Itoa(n))
	w.WriteHeader(http.StatusOK)
	for n > 0 {
		chunk := testData[:min(n, len(testData))]
		if _, err := w.Write(chunk); err != nil {
			return // the page stopped reading
		}
		n -= len(chunk)
	}
}
