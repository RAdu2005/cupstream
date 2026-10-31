package httpapi

import (
	"database/sql"
	"net/http"

	"github.com/RAdu2005/cupstream/server/internal/access"
	"github.com/RAdu2005/cupstream/server/internal/api"
	"github.com/RAdu2005/cupstream/server/internal/auth"
	"github.com/RAdu2005/cupstream/server/internal/gateway"
	"github.com/RAdu2005/cupstream/server/internal/store"
	"github.com/RAdu2005/cupstream/server/internal/view"
)

// movableStream loads cur's stream in a Discord room, the room and cur's
// link room, answering an error otherwise.
func (s *Server) movableStream(w http.ResponseWriter, r *http.Request, cur auth.Current) (ls gateway.LiveStream, from, to store.Room, ok bool) {
	ls, ok = s.Gateway.Stream(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "this stream has ended")
		return ls, from, to, false
	}
	if ls.Stream.UserID != cur.User.ID {
		writeErrorCode(w, http.StatusForbidden, string(access.NotAllowed), "only the streamer can move their stream")
		return ls, from, to, false
	}
	from, err := s.DB.GetRoom(r.Context(), ls.Stream.RoomID)
	if err == nil {
		to, err = s.DB.GetLinkRoomByOwner(r.Context(), sql.NullString{String: cur.User.ID, Valid: true})
	}
	if err != nil {
		internalError(w, r, err)
		return ls, from, to, false
	}
	if from.Kind != string(access.Discord) {
		writeError(w, http.StatusConflict, "only streams in Discord rooms can be moved")
		return ls, from, to, false
	}
	return ls, from, to, true
}

// streamViewers serves GET /api/streams/{id}/viewers: who watches the
// streamer's stream in a Discord room, to choose who follows it.
func (s *Server) streamViewers(w http.ResponseWriter, r *http.Request, cur auth.Current) {
	ls, _, to, ok := s.movableStream(w, r, cur)
	if !ok {
		return
	}
	users := view.Users{DB: s.DB}
	out := []api.StreamViewer{}
	for _, id := range s.Gateway.Viewers(ls.Stream.ID) {
		u, err := users.Get(r.Context(), id)
		if err != nil {
			internalError(w, r, err)
			return
		}
		reason, err := s.Rooms.Check(r.Context(), to, id, access.Watch)
		if err != nil {
			internalError(w, r, err)
			return
		}
		out = append(out, api.StreamViewer{User: u, Allowed: reason == access.OK})
	}
	writeJSON(w, http.StatusOK, out)
}

// moveStream serves POST /api/streams/{id}/move: the streamer moves their
// stream from a Discord room into their link room. The viewers they tick
// get a pass until the stream ends (or are allowed for good); the others
// are paused, so they can still be let in without reconnecting. The chat
// stays with the Discord room.
func (s *Server) moveStream(w http.ResponseWriter, r *http.Request, cur auth.Current) {
	var req api.MoveStreamRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Visibility != "" && req.Visibility != string(access.Everyone) {
		writeError(w, http.StatusBadRequest, "visibility is everyone or empty")
		return
	}
	ls, from, to, ok := s.movableStream(w, r, cur)
	if !ok {
		return
	}
	ctx := r.Context()
	watching := map[string]bool{}
	for _, id := range s.Gateway.Viewers(ls.Stream.ID) {
		watching[id] = true
	}

	if req.Visibility == string(access.Everyone) {
		if err := s.Rooms.SetVisibility(ctx, to, access.Everyone); err != nil {
			internalError(w, r, err)
			return
		}
		to.Visibility = sql.NullString{String: string(access.Everyone), Valid: true}
	}
	for _, v := range req.Viewers {
		if !watching[v.UserID] {
			continue // only the stream's viewers can be let in this way
		}
		var err error
		if v.Always {
			err = s.Rooms.SetAllowed(ctx, to, v.UserID, false)
		} else {
			err = s.DB.CreateStreamPass(ctx, store.CreateStreamPassParams{
				RoomID: to.ID, UserID: v.UserID, StreamID: sql.NullString{String: ls.Stream.ID, Valid: true}, Now: timeNow().Unix(),
			})
		}
		if err != nil {
			internalError(w, r, err)
			return
		}
	}
	if err := s.Gateway.MoveStream(ctx, ls.Stream.ID, to); err != nil {
		internalError(w, r, err)
		return
	}
	s.Realtime.StreamMoved(from, ls.Stream.ID, to.Slug)
	// Leaving voice no longer ends it: stop the streamer's countdown.
	s.Realtime.SendUser(cur.User.ID, api.StreamGrace{Type: "streamGrace", RoomSlug: from.Slug, StreamID: ls.Stream.ID})
	s.accessChanged(ctx, to)
	writeJSON(w, http.StatusOK, api.MoveStreamResponse{RoomSlug: to.Slug})
}
