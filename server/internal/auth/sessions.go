// Package auth handles accounts and sessions: opaque session tokens, stored
// hashed in the database, in an HttpOnly cookie for the website and as a
// Bearer token for the desktop app.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/RAdu2005/cupstream/server/internal/store"
)

const (
	sessionCookie = "cupstream_session"

	sessionTTL = 30 * 24 * time.Hour
	// A session's last_seen_at and expiry are refreshed at most this often.
	touchInterval = 10 * time.Minute
)

type Kind string

const (
	KindWeb Kind = "web"
	KindApp Kind = "app"
)

// CookieName is the name a cookie is set under. Over HTTPS it carries the
// __Host- prefix, so browsers refuse the cookie from another subdomain of
// the site (which could otherwise plant its own session or OAuth state).
// Such cookies must have Path=/ and no Domain.
func CookieName(base string, secure bool) string {
	if secure {
		return "__Host-" + base
	}
	return base
}

// Sessions creates, resolves and destroys sessions.
type Sessions struct {
	db     *store.DB
	secure bool
	name   string
	now    func() time.Time
}

func NewSessions(db *store.DB, secureCookies bool) *Sessions {
	return &Sessions{db: db, secure: secureCookies, name: CookieName(sessionCookie, secureCookies), now: time.Now}
}

// NewToken returns a random 128-bit token and its SHA-256 hash.
func NewToken() (token string, hash []byte) {
	b := make([]byte, 16)
	rand.Read(b) // never returns an error
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, HashToken(token)
}

func HashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// Start creates a website session for userID and sets the session cookie.
func (s *Sessions) Start(ctx context.Context, w http.ResponseWriter, userID string) error {
	token, err := s.create(ctx, userID, KindWeb, "")
	if err != nil {
		return err
	}
	http.SetCookie(w, s.cookie(token, int(sessionTTL.Seconds())))
	return nil
}

// StartApp creates a desktop-app session for userID and returns its token,
// which the app sends as a Bearer token.
func (s *Sessions) StartApp(ctx context.Context, userID, deviceName string) (string, error) {
	return s.create(ctx, userID, KindApp, deviceName)
}

func (s *Sessions) create(ctx context.Context, userID string, kind Kind, deviceName string) (string, error) {
	token, hash := NewToken()
	now := s.now()
	err := s.db.CreateSession(ctx, store.CreateSessionParams{
		ID:         uuid.Must(uuid.NewV7()).String(),
		TokenHash:  hash,
		UserID:     userID,
		Kind:       string(kind),
		DeviceName: deviceName,
		CreatedAt:  now.Unix(),
		ExpiresAt:  now.Add(sessionTTL).Unix(),
		LastSeenAt: now.Unix(),
	})
	return token, err
}

// End deletes the request's session, if any, and clears the cookie.
func (s *Sessions) End(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	if token := bearer(r); token != "" {
		return s.db.DeleteSessionByTokenHash(ctx, HashToken(token))
	}
	http.SetCookie(w, s.cookie("", -1))
	c, err := r.Cookie(s.name)
	if err != nil {
		return nil
	}
	return s.db.DeleteSessionByTokenHash(ctx, HashToken(c.Value))
}

// bearer returns the request's Bearer token, or "".
func bearer(r *http.Request) string {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}

func (s *Sessions) cookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     s.name,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
	}
}

// Current is the signed-in user and session of a request.
type Current struct {
	SessionID  string
	Kind       Kind
	DeviceName string // desktop-app sessions
	User       store.User
}

type ctxKey struct{}

// FromContext returns the signed-in user of the request, if any.
func FromContext(ctx context.Context) (Current, bool) {
	c, ok := ctx.Value(ctxKey{}).(Current)
	return c, ok
}

// Middleware resolves the session cookie, or the desktop app's Bearer token
// on /api/ paths, and stores the result in the request context. Requests
// without a valid session pass through anonymously.
func (s *Sessions) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Outside /api/, Bearer tokens are OBS stream keys. Inside it, a
		// stream's publish token is not a session either and simply finds
		// none.
		kind, token := KindApp, ""
		if strings.HasPrefix(r.URL.Path, "/api/") {
			token = bearer(r)
		}
		if token == "" {
			kind = KindWeb
			if c, err := r.Cookie(s.name); err == nil {
				token = c.Value
			}
		}
		if token == "" {
			next.ServeHTTP(w, r)
			return
		}
		now := s.now()
		row, err := s.db.GetSessionByTokenHash(r.Context(), store.GetSessionByTokenHashParams{
			TokenHash: HashToken(token),
			ExpiresAt: now.Unix(),
		})
		// A token is only accepted the way its kind is sent.
		if errors.Is(err, sql.ErrNoRows) || err == nil && row.Session.Kind != string(kind) {
			next.ServeHTTP(w, r)
			return
		}
		if err != nil {
			slog.ErrorContext(r.Context(), "session lookup failed", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		if now.Sub(time.Unix(row.Session.LastSeenAt, 0)) >= touchInterval {
			err := s.db.TouchSession(r.Context(), store.TouchSessionParams{
				ID:         row.Session.ID,
				LastSeenAt: now.Unix(),
				ExpiresAt:  now.Add(sessionTTL).Unix(),
			})
			if err != nil {
				slog.WarnContext(r.Context(), "session touch failed", "err", err)
			} else if kind == KindWeb {
				http.SetCookie(w, s.cookie(token, int(sessionTTL.Seconds())))
			}
		}

		ctx := context.WithValue(r.Context(), ctxKey{}, Current{SessionID: row.Session.ID, Kind: kind, DeviceName: row.Session.DeviceName, User: row.User})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// CleanupLoop deletes expired sessions every hour until ctx is done.
func (s *Sessions) CleanupLoop(ctx context.Context) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		n, err := s.db.DeleteExpiredSessions(ctx, s.now().Unix())
		if err != nil && ctx.Err() == nil {
			slog.Warn("deleting expired sessions failed", "err", err)
		} else if n > 0 {
			slog.Info("deleted expired sessions", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// List returns the user's unexpired sessions, most recently used first.
func (s *Sessions) List(ctx context.Context, userID string) ([]store.Session, error) {
	return s.db.ListSessions(ctx, store.ListSessionsParams{UserID: userID, ExpiresAt: s.now().Unix()})
}

// Revoke ends one of the user's sessions. It reports false if the session
// does not exist or belongs to someone else.
func (s *Sessions) Revoke(ctx context.Context, userID, sessionID string) (bool, error) {
	n, err := s.db.DeleteUserSession(ctx, store.DeleteUserSessionParams{ID: sessionID, UserID: userID})
	return n > 0, err
}
