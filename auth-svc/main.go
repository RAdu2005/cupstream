package main

import (
	"bufio"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/acme/autocert"
	"golang.org/x/crypto/bcrypt"
)

var (
	passwordHash []byte
	jwtSecret    []byte
	mediamtxAPI  *url.URL
	mediamtxHLS  *url.URL
	hub          *ChatHub
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "hash" {
		hashCmd()
		return
	}

	loadEnvFile(".env")
	if alt := getEnv("ENV_FILE", ""); alt != "" {
		loadEnvFile(alt)
	}

	passwordHash = []byte(mustEnv("PASSWORD_HASH"))
	jwtSecret = []byte(mustEnv("JWT_SECRET"))

	var err error
	mediamtxAPI, err = url.Parse(getEnv("MEDIAMTX_API", "http://127.0.0.1:9997"))
	check(err)
	mediamtxHLS, err = url.Parse(getEnv("MEDIAMTX_HLS", "http://127.0.0.1:8888"))
	check(err)

	hub = newChatHub()
	go hub.cleanupLoop()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/login", handleLogin)
	mux.HandleFunc("POST /api/logout", handleLogout)
	mux.HandleFunc("GET /api/auth/status", handleStatus)
	mux.Handle("GET /api/chat/history", requireAuth(http.HandlerFunc(hub.historyHandler)))
	mux.HandleFunc("GET /api/chat/ws", hub.wsHandler)

	apiProxy := &httputil.ReverseProxy{Director: mediamtxAPIDirector, ErrorHandler: proxyError}
	hlsProxy := newHLSProxy()
	mux.Handle("/api/mediamtx/", requireAuth(apiProxy))
	mux.Handle("/live/", requireAuth(hlsProxy))
	mux.Handle("/", spaHandler(distFSys()))

	port := getEnv("PORT", "443")
	useTLS := getEnv("TLS", "yes") == "yes"

	server := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	if useTLS {
		certDir := getEnv("CERT_DIR", "/var/lib/auth-svc/certs")
		cm := &autocert.Manager{
			Cache:      autocert.DirCache(certDir),
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist("cup.larp.love"),
		}
		server.TLSConfig = cm.TLSConfig()

		go func() {
			log.Printf("http→https redirect on :80")
			if err := http.ListenAndServe(":80", cm.HTTPHandler(nil)); err != nil {
				log.Fatalf("http listener: %v", err)
			}
		}()
		log.Printf("https on :%s", port)
		check(server.ListenAndServeTLS("", ""))
	}

	log.Printf("http (no TLS) on :%s — dev mode", port)
	check(server.ListenAndServe())
}

func mustEnv(k string) string {
	v := os.Getenv(k)
	if v == "" {
		log.Fatalf("missing env: %s", k)
	}
	return v
}

func hashCmd() {
	var pw string
	if len(os.Args) > 2 {
		pw = os.Args[2]
	} else {
		fmt.Fprint(os.Stderr, "password: ")
		var line string
		fmt.Scanln(&line)
		pw = line
	}
	if pw == "" {
		log.Fatal("empty password")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(pw), 14)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(h))
}

// loadEnvFile loads KEY=VALUE pairs from path into the process env.
// Existing env vars are NOT overridden. Missing file is silently ignored.
// Supports comments (#), optional `export ` prefix, and "..." / '...' quotes.
func loadEnvFile(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') && val[len(val)-1] == val[0] {
			val = val[1 : len(val)-1]
		}
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, val)
		}
	}
}

func getEnv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

var _ = signal.Notify
var _ = syscall.SIGTERM
