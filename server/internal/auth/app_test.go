package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestValidAppRedirect(t *testing.T) {
	for uri, want := range map[string]bool{
		"http://127.0.0.1:53682/callback": true,
		"http://[::1]:4000/":              true,
		"http://127.0.0.1/callback":       false, // no port
		"https://127.0.0.1:443/":          false,
		"http://localhost:8000/":          false,
		"http://10.0.0.1:8000/":           false,
		"http://evil.example:80/":         false,
		"http://u:p@127.0.0.1:80/":        false,
		"http://127.0.0.1:80/#x":          false,
		"cupstream://callback":            false,
		"":                                false,
	} {
		if got := ValidAppRedirect(uri); got != want {
			t.Errorf("ValidAppRedirect(%q) = %v, want %v", uri, got, want)
		}
	}
}

func TestCleanDeviceName(t *testing.T) {
	if got := CleanDeviceName("  my\x00 pc\n "); got != "my pc" {
		t.Errorf("got %q", got)
	}
	if got := CleanDeviceName(strings.Repeat("é", 100)); len([]rune(got)) != maxDeviceName {
		t.Errorf("not cut: %d runes", len([]rune(got)))
	}
}

func TestAppCodes(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	a := NewAppCodes()
	a.now = func() time.Time { return now }
	verifier := strings.Repeat("v", 50)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	if !ValidCodeChallenge(challenge) || ValidCodeChallenge("short") {
		t.Fatal("ValidCodeChallenge")
	}
	const redirect = "http://127.0.0.1:5000/callback"

	code := a.Issue("u1", redirect, challenge, "PC")
	if user, device, ok := a.Redeem(code, verifier, redirect); !ok || user != "u1" || device != "PC" {
		t.Fatalf("redeem = %q %q %v", user, device, ok)
	}
	if _, _, ok := a.Redeem(code, verifier, redirect); ok {
		t.Fatal("code redeemed twice")
	}

	for name, try := range map[string]func(code string) bool{
		"wrong verifier": func(c string) bool { _, _, ok := a.Redeem(c, strings.Repeat("w", 50), redirect); return ok },
		"wrong redirect": func(c string) bool { _, _, ok := a.Redeem(c, verifier, "http://127.0.0.1:5001/callback"); return ok },
		"expired": func(c string) bool {
			now = now.Add(2 * time.Minute)
			_, _, ok := a.Redeem(c, verifier, redirect)
			return ok
		},
	} {
		code := a.Issue("u1", redirect, challenge, "PC")
		if try(code) {
			t.Errorf("%s: accepted", name)
		}
		// A failed attempt uses the code up.
		if _, _, ok := a.Redeem(code, verifier, redirect); ok {
			t.Errorf("%s: code still valid after a failed attempt", name)
		}
	}
}
