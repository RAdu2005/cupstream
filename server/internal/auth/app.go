package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"
)

// The desktop app signs in like a native OAuth client (RFC 8252): it opens
// the website in the system browser, the signed-in user approves it, and the
// browser is sent back to the app's loopback address with a one-time code.
// The app exchanges the code, with its PKCE verifier, for an app session
// token that it keeps in the operating system's keychain and sends as a
// Bearer token.

const (
	// How long a code may wait to be exchanged.
	appCodeTTL = time.Minute
	// Codes waiting at once; more requests are refused until some expire.
	maxAppCodes = 10_000
	// The longest device name kept; longer ones are cut.
	maxDeviceName = 64
)

type appCode struct {
	userID      string
	redirectURI string
	challenge   string // base64url SHA-256 of the verifier (S256)
	deviceName  string
	expires     time.Time
}

// AppCodes holds the desktop app's pending sign-in codes in memory; a code
// that is lost in a restart only means signing in again.
type AppCodes struct {
	mu    sync.Mutex
	codes map[string]appCode // by code hash
	now   func() time.Time
}

func NewAppCodes() *AppCodes {
	return &AppCodes{codes: map[string]appCode{}, now: time.Now}
}

// ValidAppRedirect reports whether uri is a loopback address the desktop app
// listens on: http://127.0.0.1:<port>/... or http://[::1]:<port>/...
// (RFC 8252, section 7.3). Other hosts, including "localhost", are refused,
// so a code can never leave the user's machine.
func ValidAppRedirect(uri string) bool {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Fragment != "" || u.Port() == "" {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	return ip != nil && ip.IsLoopback() && !strings.Contains(u.Path, "..")
}

// ValidCodeChallenge reports whether c looks like an S256 PKCE challenge:
// 43 base64url characters.
func ValidCodeChallenge(c string) bool {
	b, err := base64.RawURLEncoding.DecodeString(c)
	return err == nil && len(b) == sha256.Size
}

// CleanDeviceName makes a device name the app reports safe to show: printable
// characters only, trimmed and cut to a reasonable length.
func CleanDeviceName(name string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}
		return -1
	}, name)
	name = strings.TrimSpace(name)
	if r := []rune(name); len(r) > maxDeviceName {
		name = strings.TrimSpace(string(r[:maxDeviceName]))
	}
	return name
}

// Issue returns a one-time code for userID, bound to the redirect URI and
// the PKCE challenge. It returns "" when too many codes are pending.
func (a *AppCodes) Issue(userID, redirectURI, challenge, deviceName string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	for h, c := range a.codes {
		if now.After(c.expires) {
			delete(a.codes, h)
		}
	}
	if len(a.codes) >= maxAppCodes {
		return ""
	}
	code, hash := NewToken()
	a.codes[string(hash)] = appCode{
		userID: userID, redirectURI: redirectURI, challenge: challenge,
		deviceName: CleanDeviceName(deviceName), expires: now.Add(appCodeTTL),
	}
	return code
}

// Redeem consumes a code. It succeeds when the code is valid, the redirect
// URI is the one it was issued for and the verifier matches the challenge. A
// code is consumed by the first attempt, right or wrong.
func (a *AppCodes) Redeem(code, verifier, redirectURI string) (userID, deviceName string, ok bool) {
	a.mu.Lock()
	h := string(HashToken(code))
	c, found := a.codes[h]
	delete(a.codes, h)
	a.mu.Unlock()

	if !found || a.now().After(c.expires) || c.redirectURI != redirectURI {
		return "", "", false
	}
	// RFC 7636: verifiers are 43 to 128 characters.
	if len(verifier) < 43 || len(verifier) > 128 {
		return "", "", false
	}
	sum := sha256.Sum256([]byte(verifier))
	if subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(c.challenge)) != 1 {
		return "", "", false
	}
	return c.userID, c.deviceName, true
}
