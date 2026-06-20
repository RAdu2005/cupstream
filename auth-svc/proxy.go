package main

import (
	"log"
	"net/http"
	"net/http/httputil"
	"strings"
	"time"
)

func mediamtxAPIDirector(req *http.Request) {
	req.URL.Scheme = mediamtxAPI.Scheme
	req.URL.Host = mediamtxAPI.Host
	req.URL.Path = strings.TrimPrefix(req.URL.Path, "/api/mediamtx")
	req.Host = mediamtxAPI.Host
}

func mediamtxHLSDirector(req *http.Request) {
	req.URL.Scheme = mediamtxHLS.Scheme
	req.URL.Host = mediamtxHLS.Host
	req.Host = mediamtxHLS.Host
}

func proxyError(w http.ResponseWriter, r *http.Request, err error) {
	log.Printf("proxy error %s: %v", r.URL.Path, err)
	http.Error(w, "upstream unavailable", http.StatusBadGateway)
}

// newHLSProxy returns a reverse proxy tuned for streaming:
//   - FlushInterval flushes chunks to the client promptly (LL-HLS blocking
//     playlist responses use chunked transfer-encoding)
//   - The transport's ResponseHeaderTimeout is left at 0 (unbounded) so
//     long-lived blocking requests are not cut off
func newHLSProxy() *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Director:      mediamtxHLSDirector,
		ErrorHandler:  proxyError,
		FlushInterval: 100 * time.Millisecond,
		Transport: &http.Transport{
			ResponseHeaderTimeout: 0,
			IdleConnTimeout:       120 * time.Second,
		},
	}
}
