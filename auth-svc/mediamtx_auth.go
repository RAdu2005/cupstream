package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"

	"golang.org/x/crypto/bcrypt"
)

// mtxAuthReq is the payload MediaMTX POSTs to authHTTPAddress on every
// authentication decision. Fields not relevant to the decision are ignored.
type mtxAuthReq struct {
	User     string `json:"user"`
	Password string `json:"password"`
	IP       string `json:"ip"`
	Action   string `json:"action"`
	Path     string `json:"path"`
	Protocol string `json:"protocol"`
	ID       string `json:"id"`
}

var (
	mtxPublishUser string
	mtxPublishHash []byte
)

// loadMTXAuth reads the publish credentials at startup. The hash is a
// bcrypt hash; generate it with `./auth-svc hash <password>`.
func loadMTXAuth() {
	mtxPublishUser = getEnv("MEDIAMTX_PUBLISH_USER", "radu")
	h := os.Getenv("MEDIAMTX_PUBLISH_HASH")
	if h == "" {
		log.Fatal("MEDIAMTX_PUBLISH_HASH is required (generate with: auth-svc hash <password>)")
	}
	mtxPublishHash = []byte(h)
}

// handleMTXAuth is the auth callback for MediaMTX. It runs on a dedicated
// localhost-only listener (no session cookie, no TLS) and is the only path
// where MediaMTX consults us for publish decisions.
//
// Decision policy:
//   - action == "publish": require matching user and a bcrypt-valid password.
//   - any other action (read, playback, ...): allow. Viewer WHEP requests
//     are already gated upstream by requireAuth's session-cookie check, so
//     MediaMTX sees them as unauthenticated proxy traffic that should pass.
//   - api / metrics / pprof are excluded in mediamtx.yml and never reach us.
func handleMTXAuth(w http.ResponseWriter, r *http.Request) {
	var req mtxAuthReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if req.Action != "publish" {
		w.WriteHeader(http.StatusOK)
		return
	}
	if req.User != mtxPublishUser {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err := bcrypt.CompareHashAndPassword(mtxPublishHash, []byte(req.Password)); err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	w.WriteHeader(http.StatusOK)
}
