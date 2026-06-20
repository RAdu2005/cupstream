package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

const (
	sessionMaxAge  = 24 * time.Hour
	usernameMaxLen = 32
)

type loginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type statusResp struct {
	Authenticated bool   `json:"authenticated"`
	Username      string `json:"username,omitempty"`
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	username := sanitizeUsername(req.Username)
	if username == "" {
		http.Error(w, "username required", http.StatusBadRequest)
		return
	}
	if err := bcrypt.CompareHashAndPassword(passwordHash, []byte(req.Password)); err != nil {
		time.Sleep(500 * time.Millisecond)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	tok, err := issueToken(username)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	setSessionCookie(w, tok, sessionMaxAge)
	w.WriteHeader(http.StatusNoContent)
}

func handleLogout(w http.ResponseWriter, r *http.Request) {
	clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	ck, err := r.Cookie("session")
	if err != nil || !validToken(ck.Value) {
		_ = json.NewEncoder(w).Encode(statusResp{Authenticated: false})
		return
	}
	_ = json.NewEncoder(w).Encode(statusResp{
		Authenticated: true,
		Username:      usernameFromToken(ck.Value),
	})
}

func requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ck, err := r.Cookie("session")
		if err != nil || !validToken(ck.Value) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func issueToken(username string) (string, error) {
	claims := jwt.MapClaims{
		"sub": username,
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(sessionMaxAge).Unix(),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(jwtSecret)
}

func validToken(s string) bool {
	_, err := jwt.Parse(s, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return jwtSecret, nil
	})
	return err == nil
}

func usernameFromToken(s string) string {
	tok, err := jwt.Parse(s, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return jwtSecret, nil
	})
	if err != nil || !tok.Valid {
		return ""
	}
	if c, ok := tok.Claims.(jwt.MapClaims); ok {
		if sub, ok := c["sub"].(string); ok {
			return sub
		}
	}
	return ""
}

func setSessionCookie(w http.ResponseWriter, v string, maxAge time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name: "session", Value: v, Path: "/",
		HttpOnly: true, Secure: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge: int(maxAge.Seconds()),
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: "session", Value: "", Path: "/", MaxAge: -1,
	})
}

func sanitizeUsername(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > usernameMaxLen {
		return ""
	}
	var b strings.Builder
	for _, r := range s {
		if unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}
