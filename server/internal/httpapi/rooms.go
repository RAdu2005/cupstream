package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/RAdu2005/cupstream/server/internal/access"
	"github.com/RAdu2005/cupstream/server/internal/api"
	"github.com/RAdu2005/cupstream/server/internal/auth"
	"github.com/RAdu2005/cupstream/server/internal/gateway"
	"github.com/RAdu2005/cupstream/server/internal/store"
	"github.com/RAdu2005/cupstream/server/internal/view"
)

var timeNow = time.Now

// loadRoom finds the room in the path, answering 404 if there is none.
func (s *Server) loadRoom(w http.ResponseWriter, r *http.Request) (store.Room, bool) {
	room, err := s.DB.GetRoomBySlug(r.Context(), r.PathValue("slug"))
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "this room does not exist")
		return room, false
	}
	if err != nil {
		internalError(w, r, err)
		return room, false
	}
	return room, true
}

// ownRoom loads a link room owned by cur, answering 403 otherwise.
func (s *Server) ownRoom(w http.ResponseWriter, r *http.Request, cur auth.Current) (store.Room, bool) {
	room, ok := s.loadRoom(w, r)
	if !ok {
		return room, false
	}
	if room.Kind != string(access.Link) || room.OwnerID.String != cur.User.ID {
		writeErrorCode(w, http.StatusForbidden, string(access.NotAllowed), "only the room's owner can change this")
		return room, false
	}
	return room, true
}

// accessChanged applies a change to room's access rules to the media
// sessions and realtime subscriptions in it.
func (s *Server) accessChanged(ctx context.Context, room store.Room) {
	s.Gateway.Reevaluate(ctx, room)
	s.Realtime.Reevaluate(ctx, room)
}

func refusal(w http.ResponseWriter, reason access.Reason, msg string) {
	status := http.StatusForbidden
	if reason == access.SignInRequired {
		status = http.StatusUnauthorized
	}
	writeErrorCode(w, status, string(reason), msg)
}

func (s *Server) getRoom(w http.ResponseWriter, r *http.Request) {
	room, ok := s.loadRoom(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	perms, err := s.Rooms.Permissions(ctx, room, userID(r))
	if err != nil {
		internalError(w, r, err)
		return
	}
	if perms.Watch != access.OK {
		refusal(w, perms.Watch, "you cannot enter this room")
		return
	}

	out := api.Room{
		ID: room.ID, Slug: room.Slug, Kind: room.Kind, Visibility: room.Visibility.String,
		Permissions: api.Permissions{
			Watch:           true,
			Stream:          perms.Stream == access.OK,
			Chat:            perms.Chat == access.OK,
			ModerateStreams: perms.ModerateStreams == access.OK,
			ModerateChat:    perms.ModerateChat == access.OK,
		},
		Streams: []api.Stream{},
	}
	users := view.Users{DB: s.DB}
	if room.OwnerID.Valid {
		owner, err := users.Get(ctx, room.OwnerID.String)
		if err != nil {
			internalError(w, r, err)
			return
		}
		out.Owner = &owner
	}
	for _, ls := range s.Gateway.RoomStreams(room.ID) {
		st, err := view.Stream(ctx, &users, ls)
		if err != nil {
			internalError(w, r, err)
			return
		}
		out.Streams = append(out.Streams, st)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) updateRoom(w http.ResponseWriter, r *http.Request, cur auth.Current) {
	room, ok := s.ownRoom(w, r, cur)
	if !ok {
		return
	}
	var req api.UpdateRoomRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	v := access.Visibility(req.Visibility)
	if v != access.Everyone && v != access.SignedIn && v != access.InviteOnly {
		writeError(w, http.StatusBadRequest, "visibility is everyone, signed_in or invite_only")
		return
	}
	if err := s.Rooms.SetVisibility(r.Context(), room, v); err != nil {
		internalError(w, r, err)
		return
	}
	room.Visibility = sql.NullString{String: string(v), Valid: true}
	s.accessChanged(r.Context(), room)
	writeJSON(w, http.StatusOK, req)
}

func (s *Server) listAllowed(w http.ResponseWriter, r *http.Request, cur auth.Current) {
	room, ok := s.ownRoom(w, r, cur)
	if !ok {
		return
	}
	rows, err := s.DB.ListAllowed(r.Context(), room.ID)
	if err != nil {
		internalError(w, r, err)
		return
	}
	out := []api.AllowedEntry{}
	for _, row := range rows {
		out = append(out, api.AllowedEntry{User: view.User(row.User), CanStream: row.CanStream})
	}
	writeJSON(w, http.StatusOK, out)
}

// allowedTarget loads the room and the user named in the path.
func (s *Server) allowedTarget(w http.ResponseWriter, r *http.Request, cur auth.Current) (store.Room, store.User, bool) {
	room, ok := s.ownRoom(w, r, cur)
	if !ok {
		return room, store.User{}, false
	}
	u, err := s.DB.GetUserByHandle(r.Context(), r.PathValue("handle"))
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "no user has this handle")
		return room, u, false
	}
	if err != nil {
		internalError(w, r, err)
		return room, u, false
	}
	if u.ID == cur.User.ID {
		writeError(w, http.StatusBadRequest, "you are the owner of this room")
		return room, u, false
	}
	return room, u, true
}

func (s *Server) setAllowed(w http.ResponseWriter, r *http.Request, cur auth.Current) {
	room, u, ok := s.allowedTarget(w, r, cur)
	if !ok {
		return
	}
	var req api.SetAllowedRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.Rooms.SetAllowed(r.Context(), room, u.ID, req.CanStream); err != nil {
		internalError(w, r, err)
		return
	}
	s.accessChanged(r.Context(), room) // a co-streamer may have lost streaming
	writeJSON(w, http.StatusOK, api.AllowedEntry{User: view.User(u), CanStream: req.CanStream})
}

func (s *Server) removeAllowed(w http.ResponseWriter, r *http.Request, cur auth.Current) {
	room, u, ok := s.allowedTarget(w, r, cur)
	if !ok {
		return
	}
	if _, err := s.Rooms.RemoveAllowed(r.Context(), room, u.ID); err != nil {
		internalError(w, r, err)
		return
	}
	s.accessChanged(r.Context(), room)
	w.WriteHeader(http.StatusNoContent)
}

// startStream serves POST /api/streams for app and web senders.
func (s *Server) startStream(w http.ResponseWriter, r *http.Request, cur auth.Current) {
	var req api.StartStreamRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	src := gateway.Source(req.Source)
	if src != gateway.SourceApp && src != gateway.SourceWeb {
		writeError(w, http.StatusBadRequest, "source is app or web; OBS publishes to /whip with its stream key")
		return
	}
	started, err := s.Gateway.Start(r.Context(), gateway.StartRequest{
		User: cur.User, Source: src, RoomSlug: req.Room, LinkFallback: req.LinkFallback, Replace: req.Replace,
	})
	if err != nil {
		s.Gateway.WriteStartError(w, r, err)
		return
	}
	users := view.Users{DB: s.DB}
	st, err := view.Stream(r.Context(), &users, gateway.LiveStream{Stream: started.Stream, State: gateway.Starting})
	if err != nil {
		internalError(w, r, err)
		return
	}
	whip := url.URL{Scheme: s.Config.PublicURL.Scheme, Host: s.Config.PublicURL.Host, Path: "/api/streams/" + started.Stream.ID + "/whip"}
	writeJSON(w, http.StatusCreated, api.StartStreamResponse{
		Stream: st, RoomSlug: started.Room.Slug, WhipURL: whip.String(), Token: started.Token,
	})
}

// endStream serves DELETE /api/streams/{id}: the streamer stops, or a
// moderator of the room ends someone else's stream.
func (s *Server) endStream(w http.ResponseWriter, r *http.Request, cur auth.Current) {
	ls, ok := s.Gateway.Stream(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "this stream has ended")
		return
	}
	reason := gateway.EndStopped
	if ls.Stream.UserID != cur.User.ID {
		room, err := s.DB.GetRoom(r.Context(), ls.Stream.RoomID)
		if err != nil {
			internalError(w, r, err)
			return
		}
		verdict, err := s.Rooms.Check(r.Context(), room, cur.User.ID, access.ModerateStreams)
		if err != nil {
			internalError(w, r, err)
			return
		}
		if verdict != access.OK {
			refusal(w, verdict, "you cannot end this stream")
			return
		}
		reason = gateway.EndModerator
	}
	s.Gateway.EndStream(r.Context(), ls.Stream.ID, reason)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) play(w http.ResponseWriter, r *http.Request) {
	s.Gateway.Play(w, r, userID(r))
}

func (s *Server) thumbnail(w http.ResponseWriter, r *http.Request) {
	s.Gateway.HandleThumbnail(w, r, userID(r))
}
