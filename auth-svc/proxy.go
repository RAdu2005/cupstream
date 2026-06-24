package main

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

func mediamtxAPIDirector(req *http.Request) {
	req.URL.Scheme = mediamtxAPI.Scheme
	req.URL.Host = mediamtxAPI.Host
	req.URL.Path = strings.TrimPrefix(req.URL.Path, "/api/mediamtx")
	req.Host = mediamtxAPI.Host
}

// mediamtxWebRTCDirector forwards WHEP signaling (POST/PATCH/DELETE on /live/whep)
// to MediaMTX's WebRTC listener. The full path is preserved so MediaMTX sees
// /<path>/whep[/<id>] exactly as it expects.
func mediamtxWebRTCDirector(req *http.Request) {
	req.URL.Scheme = mediamtxWebRTC.Scheme
	req.URL.Host = mediamtxWebRTC.Host
	req.Host = mediamtxWebRTC.Host
}

func proxyError(w http.ResponseWriter, r *http.Request, err error) {
	log.Printf("proxy error %s: %v", r.URL.Path, err)
	http.Error(w, "upstream unavailable", http.StatusBadGateway)
}

// newWHEPProxy returns a reverse proxy tuned for WebRTC-WHEP signaling:
//   - FlushInterval flushes chunks to the client promptly
//   - ResponseHeaderTimeout is unbounded so long-lived PATCH (ICE trickle)
//     requests are not cut off
//   - ModifyResponse rewrites the absolute Location header that MediaMTX
//     returns on the 201 Created response, so subsequent PATCH/DELETE
//     from the browser target our proxy instead of the MediaMTX host
func newWHEPProxy() *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Director:      mediamtxWebRTCDirector,
		ErrorHandler:  proxyError,
		FlushInterval: 100 * time.Millisecond,
		Transport: &http.Transport{
			ResponseHeaderTimeout: 0,
			IdleConnTimeout:       120 * time.Second,
		},
		ModifyResponse: rewriteWHEPLocation,
	}
}

// rewriteWHEPLocation rewrites the backend's absolute Location header
// (e.g. http://127.0.0.1:8889/live/whep/<id>) to use the request's
// scheme+host while preserving the path, so the browser's PATCH/DELETE
// on the WHEP resource hits our proxy on the same origin.
func rewriteWHEPLocation(resp *http.Response) error {
	loc := resp.Header.Get("Location")
	if loc == "" || resp.Request == nil {
		return nil
	}
	u, err := url.Parse(loc)
	if err != nil {
		return nil
	}
	if u.Host == "" {
		return nil
	}
	if resp.Request.TLS != nil ||
		strings.EqualFold(resp.Request.Header.Get("X-Forwarded-Proto"), "https") {
		u.Scheme = "https"
	} else {
		u.Scheme = "http"
	}
	u.Host = resp.Request.Host
	resp.Header.Set("Location", u.String())
	return nil
}
