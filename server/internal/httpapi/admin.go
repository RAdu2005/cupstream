package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"

	"github.com/RAdu2005/cupstream/server/internal/api"
	"github.com/RAdu2005/cupstream/server/internal/auth"
	"github.com/RAdu2005/cupstream/server/internal/gateway"
	"github.com/RAdu2005/cupstream/server/internal/store"
	"github.com/RAdu2005/cupstream/server/internal/view"
)

// Admins (CUPSTREAM_ADMINS, by sign-in identity) see every live stream and
// what it costs, can end any stream, and can ban users platform-wide
// (overview 9.1 and 9.4).

func (s *Server) adminRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/admin", s.requireAdmin(s.adminOverview))
	mux.Handle("POST /api/admin/streams/{id}/end", s.requireAdmin(s.adminEndStream))
	mux.Handle("PUT /api/admin/bans/{handle}", s.requireAdmin(s.platformBan))
	mux.Handle("DELETE /api/admin/bans/{handle}", s.requireAdmin(s.platformUnban))
}

// isAdmin reports whether one of the user's sign-in identities is listed in
// CUPSTREAM_ADMINS.
func (s *Server) isAdmin(ctx context.Context, userID string) (bool, error) {
	if len(s.Config.Admins) == 0 {
		return false, nil
	}
	ids, err := s.DB.ListIdentities(ctx, userID)
	if err != nil {
		return false, err
	}
	for _, id := range ids {
		if s.Config.Admins[id.Provider+":"+id.Subject] {
			return true, nil
		}
	}
	return false, nil
}

// requireAdmin answers 401 without a signed-in user and 403 unless they are
// an admin.
func (s *Server) requireAdmin(h func(http.ResponseWriter, *http.Request, auth.Current)) http.Handler {
	return requireUser(func(w http.ResponseWriter, r *http.Request, cur auth.Current) {
		ok, err := s.isAdmin(r.Context(), cur.User.ID)
		if err != nil {
			internalError(w, r, err)
			return
		}
		if !ok {
			writeErrorCode(w, http.StatusForbidden, "admin_only", "only admins may do this")
			return
		}
		h(w, r, cur)
	})
}

func (s *Server) adminOverview(w http.ResponseWriter, r *http.Request, cur auth.Current) {
	out := api.AdminOverview{Streams: []api.AdminStream{}, Banned: []api.BannedUser{}}
	users := view.Users{DB: s.DB}
	slugs := map[string]string{} // by room ID
	for _, as := range s.Gateway.AllStreams() {
		st, err := view.Stream(r.Context(), &users, as.LiveStream)
		if err != nil {
			internalError(w, r, err)
			return
		}
		slug, ok := slugs[as.Stream.RoomID]
		if !ok {
			room, err := s.DB.GetRoom(r.Context(), as.Stream.RoomID)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				internalError(w, r, err)
				return
			}
			slug = room.Slug
			slugs[as.Stream.RoomID] = slug
		}
		out.Streams = append(out.Streams, api.AdminStream{Stream: st, RoomSlug: slug, Outgoing: as.Outgoing})
		out.Viewers += as.Watching
		out.Incoming += as.Stats.Bitrate
		out.Outgoing += as.Outgoing
	}
	banned, err := s.DB.ListBannedUsers(r.Context())
	if err != nil {
		internalError(w, r, err)
		return
	}
	for _, u := range banned {
		out.Banned = append(out.Banned, api.BannedUser{User: view.User(u), BannedAt: u.BannedAt.Int64})
	}
	writeJSON(w, http.StatusOK, out)
}

// adminEndStream serves POST /api/admin/streams/{id}/end: the kill switch.
func (s *Server) adminEndStream(w http.ResponseWriter, r *http.Request, cur auth.Current) {
	ls, ok := s.Gateway.Stream(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "this stream has ended")
		return
	}
	s.Gateway.EndStream(r.Context(), ls.Stream.ID, gateway.EndAdmin)
	slog.InfoContext(r.Context(), "admin ended a stream", "admin", cur.User.ID, "stream", ls.Stream.ID, "user", ls.Stream.UserID)
	w.WriteHeader(http.StatusNoContent)
}

// platformBanTarget loads the user named in the path, refusing admins:
// they are removed from CUPSTREAM_ADMINS first.
func (s *Server) platformBanTarget(w http.ResponseWriter, r *http.Request) (store.User, bool) {
	u, err := s.DB.GetUserByHandle(r.Context(), r.PathValue("handle"))
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "no user has this handle")
		return u, false
	}
	if err != nil {
		internalError(w, r, err)
		return u, false
	}
	admin, err := s.isAdmin(r.Context(), u.ID)
	if err != nil {
		internalError(w, r, err)
		return u, false
	}
	if admin {
		writeErrorCode(w, http.StatusConflict, "admin", "admins cannot be banned; remove them from CUPSTREAM_ADMINS first")
		return u, false
	}
	return u, true
}

// platformBan serves PUT /api/admin/bans/{handle}: a ban from the whole
// platform, unlike a room ban (bans.go). The user's sessions are revoked,
// their streams ended, their viewer sessions and sockets closed, and they
// cannot sign in again. Watching rooms open to everyone, signed out, cannot
// be prevented.
func (s *Server) platformBan(w http.ResponseWriter, r *http.Request, cur auth.Current) {
	u, ok := s.platformBanTarget(w, r)
	if !ok {
		return
	}
	if !u.BannedAt.Valid {
		err := s.DB.Tx(r.Context(), func(q *store.Queries) error {
			if err := q.SetUserBanned(r.Context(), store.SetUserBannedParams{
				BannedAt: sql.NullInt64{Int64: timeNow().Unix(), Valid: true}, ID: u.ID,
			}); err != nil {
				return err
			}
			_, err := q.DeleteAllUserSessions(r.Context(), u.ID)
			return err
		})
		if err != nil {
			internalError(w, r, err)
			return
		}
	}
	// Again even if banned already, in case a previous attempt stopped halfway.
	s.Realtime.CloseUser(u.ID)
	s.Gateway.EndUserStreams(r.Context(), u.ID, gateway.EndBanned)
	s.Gateway.CloseUserViewers(r.Context(), u.ID)
	slog.InfoContext(r.Context(), "admin banned a user", "admin", cur.User.ID, "user", u.ID)
	w.WriteHeader(http.StatusNoContent)
}

// platformUnban serves DELETE /api/admin/bans/{handle}.
func (s *Server) platformUnban(w http.ResponseWriter, r *http.Request, cur auth.Current) {
	u, err := s.DB.GetUserByHandle(r.Context(), r.PathValue("handle"))
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "no user has this handle")
		return
	}
	if err != nil {
		internalError(w, r, err)
		return
	}
	if err := s.DB.SetUserBanned(r.Context(), store.SetUserBannedParams{ID: u.ID}); err != nil {
		internalError(w, r, err)
		return
	}
	slog.InfoContext(r.Context(), "admin lifted a ban", "admin", cur.User.ID, "user", u.ID)
	w.WriteHeader(http.StatusNoContent)
}
