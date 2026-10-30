// Package config loads the server configuration from CUPSTREAM_* environment
// variables.
package config

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// OAuthClient holds an OAuth application's credentials. The provider is
// enabled when both are set.
type OAuthClient struct {
	ID, Secret string
}

func (c OAuthClient) Enabled() bool { return c.ID != "" }

type Config struct {
	// Addr is the listen address of the public HTTP server.
	Addr string
	// PublicURL is the origin users open in the browser. It decides whether
	// cookies are Secure and, later, the OAuth redirect URLs.
	PublicURL *url.URL
	// ClientIPHeader names a request header that carries the client's
	// address, such as CF-Connecting-IP behind a Cloudflare Tunnel. It is
	// trusted only on connections from this machine (the tunnel connector).
	ClientIPHeader string
	// TLSCert and TLSKey, when both set, make the server speak HTTPS
	// (a Cloudflare Origin CA certificate in production).
	TLSCert, TLSKey string

	DBPath string

	MediaMTXAPI    *url.URL
	MediaMTXWebRTC *url.URL

	// DevLogin enables the passwordless development login. It is refused
	// unless PublicURL is a local development host.
	DevLogin bool
	// NoRateLimits turns off the per-address rate limits, for load tests
	// from one machine (CUPSTREAM_RATE_LIMITS=false). Like DevLogin, it is
	// refused unless PublicURL is a local development host.
	NoRateLimits bool

	// SecretKey (32 bytes) seals OAuth cookies and stored Discord refresh
	// tokens. Required unless DevLogin is set, in which case a random key is
	// used when it is missing.
	SecretKey          []byte
	SecretKeyGenerated bool

	Google  OAuthClient
	Discord OAuthClient

	LogLevel  slog.Level
	LogFormat string // "json" or "text"
}

// Load reads the configuration from the process environment.
func Load() (*Config, error) { return load(os.Getenv) }

func load(getenv func(string) string) (*Config, error) {
	get := func(key, def string) string {
		if v := getenv("CUPSTREAM_" + key); v != "" {
			return v
		}
		return def
	}
	var errs []error
	parseURL := func(key, def string) *url.URL {
		raw := get(key, def)
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, fmt.Errorf("CUPSTREAM_%s: %q is not an http(s) URL", key, raw))
			return &url.URL{}
		}
		return u
	}
	parseBool := func(key string, def bool) bool {
		raw := get(key, strconv.FormatBool(def))
		b, err := strconv.ParseBool(raw)
		if err != nil {
			errs = append(errs, fmt.Errorf("CUPSTREAM_%s: %q is not a boolean", key, raw))
		}
		return b
	}

	c := &Config{
		Addr:           get("ADDR", ":8080"),
		PublicURL:      parseURL("PUBLIC_URL", "http://localhost:8080"),
		ClientIPHeader: get("CLIENT_IP_HEADER", ""),
		TLSCert:        get("TLS_CERT", ""),
		TLSKey:         get("TLS_KEY", ""),
		DBPath:         get("DB", "data/cupstream.db"),
		MediaMTXAPI:    parseURL("MEDIAMTX_API", "http://127.0.0.1:9997"),
		MediaMTXWebRTC: parseURL("MEDIAMTX_WEBRTC", "http://127.0.0.1:8889"),
		DevLogin:       parseBool("DEV_LOGIN", false),
		NoRateLimits:   !parseBool("RATE_LIMITS", true),
		LogFormat:      get("LOG_FORMAT", "json"),
		Google:         OAuthClient{ID: get("GOOGLE_CLIENT_ID", ""), Secret: get("GOOGLE_CLIENT_SECRET", "")},
		Discord:        OAuthClient{ID: get("DISCORD_CLIENT_ID", ""), Secret: get("DISCORD_CLIENT_SECRET", "")},
	}

	for name, oc := range map[string]OAuthClient{"GOOGLE": c.Google, "DISCORD": c.Discord} {
		if (oc.ID == "") != (oc.Secret == "") {
			errs = append(errs, fmt.Errorf("CUPSTREAM_%s_CLIENT_ID and CUPSTREAM_%s_CLIENT_SECRET must be set together", name, name))
		}
	}

	if raw := get("SECRET_KEY", ""); raw != "" {
		key, err := base64.StdEncoding.DecodeString(raw)
		if err != nil || len(key) != 32 {
			errs = append(errs, errors.New("CUPSTREAM_SECRET_KEY must be 32 bytes, base64-encoded (openssl rand -base64 32)"))
		}
		c.SecretKey = key
	} else if c.DevLogin {
		c.SecretKey = make([]byte, 32)
		rand.Read(c.SecretKey)
		c.SecretKeyGenerated = true
	} else {
		errs = append(errs, errors.New("CUPSTREAM_SECRET_KEY is required (generate one with: openssl rand -base64 32)"))
	}

	if err := c.LogLevel.UnmarshalText([]byte(get("LOG_LEVEL", "info"))); err != nil {
		errs = append(errs, fmt.Errorf("CUPSTREAM_LOG_LEVEL: %w", err))
	}
	if c.LogFormat != "json" && c.LogFormat != "text" {
		errs = append(errs, fmt.Errorf("CUPSTREAM_LOG_FORMAT: %q must be json or text", c.LogFormat))
	}
	if (c.TLSCert == "") != (c.TLSKey == "") {
		errs = append(errs, errors.New("CUPSTREAM_TLS_CERT and CUPSTREAM_TLS_KEY must be set together"))
	}
	if c.DevLogin && c.PublicURL.Host != "" && !IsLocalHost(c.PublicURL.Hostname()) {
		errs = append(errs, fmt.Errorf("CUPSTREAM_DEV_LOGIN is only allowed when CUPSTREAM_PUBLIC_URL is a local host, not %q", c.PublicURL.Hostname()))
	}
	if c.NoRateLimits && c.PublicURL.Host != "" && !IsLocalHost(c.PublicURL.Hostname()) {
		errs = append(errs, fmt.Errorf("CUPSTREAM_RATE_LIMITS can only be turned off when CUPSTREAM_PUBLIC_URL is a local host, not %q", c.PublicURL.Hostname()))
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return c, nil
}

// SecureCookies reports whether cookies must carry the Secure attribute.
func (c *Config) SecureCookies() bool { return c.PublicURL.Scheme == "https" }

// IsLocalHost reports whether host (without port) is a local development host:
// localhost, a loopback address, or a name ending in .localhost or .test.
func IsLocalHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".test") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}
