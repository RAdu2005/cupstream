package httpapi

import (
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/RAdu2005/cupstream/server/internal/ratelimit"
)

// Requests per client address (the visitor's, behind the tunnel), for each
// kind of request: a burst at once, then one more per refill. Friends behind
// one home connection share them, so they are generous: they stop scripts,
// not people.
const (
	// Signing in: OAuth start and callback, and the development login.
	signInBurst  = 20
	signInRefill = 3 * time.Second
	// Going live: POST /api/streams, and WHIP from OBS and the apps.
	publishBurst  = 10
	publishRefill = 6 * time.Second
	// Opening viewer sessions (WHEP); a room page opens one per live stream.
	playBurst  = 60
	playRefill = 500 * time.Millisecond
	// Opening the realtime WebSocket; tabs reconnect with backoff.
	connectBurst  = 30
	connectRefill = time.Second
	// Every other change through the API.
	writeBurst  = 60
	writeRefill = 250 * time.Millisecond
)

// rateLimits answers 429 to a client address that sends too many requests
// of one kind. Requests are sorted into kinds by a ServeMux of their own, so
// routing in Handler is unaffected. Media session requests (/api/media/...)
// are not limited: they need the session's secret, and browsers send several
// trickle-ICE PATCHes for every session.
func rateLimits(enabled bool, now func() time.Time, next http.Handler) http.Handler {
	if !enabled {
		return next
	}
	kinds := http.NewServeMux()
	limiters := map[string]*ratelimit.Limiter{} // by pattern
	kind := func(l *ratelimit.Limiter, patterns ...string) {
		for _, p := range patterns {
			kinds.Handle(p, next)
			limiters[p] = l
		}
	}
	kind(ratelimit.New(signInBurst, signInRefill),
		"GET /api/auth/{provider}/start", "GET /api/auth/{provider}/callback", "POST /api/auth/dev/login")
	kind(ratelimit.New(publishBurst, publishRefill),
		"POST /api/streams", "POST /whip", "POST /api/streams/{id}/whip")
	kind(ratelimit.New(playBurst, playRefill), "POST /api/streams/{id}/whep")
	kind(ratelimit.New(connectBurst, connectRefill), "GET /api/realtime")
	kind(ratelimit.New(writeBurst, writeRefill), "POST /api/", "PUT /api/", "PATCH /api/", "DELETE /api/")
	kind(nil, "POST /api/media/", "PATCH /api/media/", "DELETE /api/media/", "/")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, pattern := kinds.Handler(r)
		if l := limiters[pattern]; l != nil && !l.Allow(ratelimit.AddrKey(r.RemoteAddr), now()) {
			w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(l.Refill().Seconds()))))
			writeErrorCode(w, http.StatusTooManyRequests, "rate_limited", "too many requests, try again in a moment")
			return
		}
		next.ServeHTTP(w, r)
	})
}
