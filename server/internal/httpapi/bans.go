package httpapi

import (
	"net/http"

	"github.com/RAdu2005/cupstream/server/internal/api"
	"github.com/RAdu2005/cupstream/server/internal/auth"
	"github.com/RAdu2005/cupstream/server/internal/store"
	"github.com/RAdu2005/cupstream/server/internal/view"
)

func (s *Server) banRoutes(mux *http.ServeMux) {
	mux.Handle("POST /api/rooms/{slug}/kick/{handle}", requireUser(s.kick))
	mux.Handle("GET /api/rooms/{slug}/bans", requireUser(s.listBans))
	mux.Handle("PUT /api/rooms/{slug}/bans/{handle}", requireUser(s.ban))
	mux.Handle("DELETE /api/rooms/{slug}/bans/{handle}", requireUser(s.unban))
}

// removeFromRoom ends u's sessions and subscriptions in room after a kick
// or ban, then applies the access change (a co-streamer's stream may end).
func (s *Server) removeFromRoom(r *http.Request, room store.Room, u store.User, code string) {
	s.Realtime.RemoveUser(room, u.ID, code)
	s.Gateway.CloseViewerSessions(r.Context(), room.ID, u.ID)
	s.accessChanged(r.Context(), room)
}

// kick serves POST /api/rooms/{slug}/kick/{handle}: the owner ends someone's
// sessions and temporary access. If the room still lets them in (its
// visibility or the allowed list), they can come back.
func (s *Server) kick(w http.ResponseWriter, r *http.Request, cur auth.Current) {
	room, u, ok := s.allowedTarget(w, r, cur)
	if !ok {
		return
	}
	if err := s.Rooms.Kick(r.Context(), room, u.ID); err != nil {
		internalError(w, r, err)
		return
	}
	s.removeFromRoom(r, room, u, "kicked")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listBans(w http.ResponseWriter, r *http.Request, cur auth.Current) {
	room, ok := s.ownRoom(w, r, cur)
	if !ok {
		return
	}
	rows, err := s.DB.ListBans(r.Context(), room.ID)
	if err != nil {
		internalError(w, r, err)
		return
	}
	out := []api.BanEntry{}
	for _, row := range rows {
		out = append(out, api.BanEntry{User: view.User(row.User), BannedAt: row.BannedAt})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) ban(w http.ResponseWriter, r *http.Request, cur auth.Current) {
	room, u, ok := s.allowedTarget(w, r, cur)
	if !ok {
		return
	}
	if err := s.Rooms.Ban(r.Context(), room, u.ID); err != nil {
		internalError(w, r, err)
		return
	}
	s.removeFromRoom(r, room, u, "banned")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) unban(w http.ResponseWriter, r *http.Request, cur auth.Current) {
	room, u, ok := s.allowedTarget(w, r, cur)
	if !ok {
		return
	}
	found, err := s.Rooms.Unban(r.Context(), room, u.ID)
	if err != nil {
		internalError(w, r, err)
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "this person is not banned")
		return
	}
	s.accessChanged(r.Context(), room)
	w.WriteHeader(http.StatusNoContent)
}
