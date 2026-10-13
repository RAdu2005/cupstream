// Package auth handles accounts and sessions: opaque session tokens in an
// HttpOnly cookie, stored hashed in the database.
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
	"time"

	"github.com/google/uuid"

	"github.com/RAdu2005/cupstream/server/internal/store"
)

const (
	CookieName = "cupstream_session"

	sessionTTL = 30 * 24 * time.Hour
	// A session's last_seen_at and expiry are refreshed at most this often.
	touchInterval = 10 * time.Minute
)

type Kind string

const (
	KindWeb Kind = "web"
	KindApp Kind = "app"
)

// Sessions creates, resolves and destroys sessions.
type Sessions struct {
	db     *store.DB
	secure bool
	now    func() time.Time
}

func NewSessions(db *store.DB, secureCookies bool) *Sessions {
	return &Sessions{db: db, secure: secureCookies, now: time.Now}
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

// Start creates a session for userID and sets the session cookie.
func (s *Sessions) Start(ctx context.Context, w http.ResponseWriter, userID string, kind Kind, deviceName string) error {
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
	if err != nil {
		return err
	}
	http.SetCookie(w, s.cookie(token, int(sessionTTL.Seconds())))
	return nil
}

// End deletes the request's session, if any, and clears the cookie.
func (s *Sessions) End(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	http.SetCookie(w, s.cookie("", -1))
	c, err := r.Cookie(CookieName)
	if err != nil {
		return nil
	}
	return s.db.DeleteSessionByTokenHash(ctx, HashToken(c.Value))
}

func (s *Sessions) cookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     CookieName,
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
	SessionID string
	User      store.User
}

type ctxKey struct{}

// FromContext returns the signed-in user of the request, if any.
func FromContext(ctx context.Context) (Current, bool) {
	c, ok := ctx.Value(ctxKey{}).(Current)
	return c, ok
}

// Middleware resolves the session cookie and stores the result in the request
// context. Requests without a valid session pass through anonymously.
func (s *Sessions) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(CookieName)
		if err != nil || c.Value == "" {
			next.ServeHTTP(w, r)
			return
		}
		now := s.now()
		row, err := s.db.GetSessionByTokenHash(r.Context(), store.GetSessionByTokenHashParams{
			TokenHash: HashToken(c.Value),
			ExpiresAt: now.Unix(),
		})
		if errors.Is(err, sql.ErrNoRows) {
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
			} else if row.Session.Kind == string(KindWeb) {
				http.SetCookie(w, s.cookie(c.Value, int(sessionTTL.Seconds())))
			}
		}

		ctx := context.WithValue(r.Context(), ctxKey{}, Current{SessionID: row.Session.ID, User: row.User})
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
