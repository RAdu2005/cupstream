package httpapi

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/RAdu2005/cupstream/server/internal/access"
	"github.com/RAdu2005/cupstream/server/internal/api"
	"github.com/RAdu2005/cupstream/server/internal/auth"
	"github.com/RAdu2005/cupstream/server/internal/friends"
	"github.com/RAdu2005/cupstream/server/internal/view"
)

func (s *Server) friendsRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/friends", requireUser(s.listFriends))
	mux.Handle("GET /api/friends/lookup", requireUser(s.lookupUser))
	mux.Handle("POST /api/friends/requests", requireUser(s.sendFriendRequest))
	mux.Handle("POST /api/friends/requests/{id}/accept", requireUser(s.acceptFriendRequest))
	mux.Handle("DELETE /api/friends/requests/{id}", requireUser(s.removeFriendRequest))
	mux.Handle("DELETE /api/friends/{id}", requireUser(s.unfriend))
	mux.Handle("GET /api/friends/live", requireUser(s.friendsLive))
	mux.Handle("GET /api/friends/suggestions", requireUser(s.listSuggestions))
	mux.Handle("DELETE /api/friends/suggestions/{id}", requireUser(s.dismissSuggestion))
	mux.Handle("GET /api/friends/blocked", requireUser(s.listBlocked))
	mux.Handle("PUT /api/friends/blocked/{id}", requireUser(s.block))
	mux.Handle("DELETE /api/friends/blocked/{id}", requireUser(s.unblock))
}

// friendError answers a failed friend action.
func friendError(w http.ResponseWriter, r *http.Request, err error) {
	var limit *friends.LimitError
	switch {
	case errors.Is(err, friends.ErrSelf):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, friends.ErrNoUser), errors.Is(err, friends.ErrNoRequest), errors.Is(err, friends.ErrNotFriends):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, friends.ErrYouBlocked):
		writeErrorCode(w, http.StatusConflict, "blocked", err.Error())
	case errors.As(err, &limit):
		writeErrorCode(w, http.StatusConflict, "limit", err.Error())
	case errors.Is(err, friends.ErrRateLimited):
		writeErrorCode(w, http.StatusTooManyRequests, "rate_limited", err.Error())
	default:
		internalError(w, r, err)
	}
}

// friendsChanged tells these users' tabs to fetch their friends again.
func (s *Server) friendsChanged(userIDs ...string) {
	s.Realtime.NotifyUsers(api.FriendsChanged{Type: "friendsChanged"}, userIDs...)
}

func entries[T any](in []friends.Entry, f func(api.User, int64) T) []T {
	out := make([]T, 0, len(in))
	for _, e := range in {
		out = append(out, f(view.User(e.User), e.Since))
	}
	return out
}

func (s *Server) listFriends(w http.ResponseWriter, r *http.Request, cur auth.Current) {
	list, err := s.Friends.List(r.Context(), cur.User.ID)
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, api.Friends{
		Friends:  entries(list.Friends, func(u api.User, t int64) api.Friend { return api.Friend{User: u, Since: t} }),
		Incoming: entries(list.Incoming, func(u api.User, t int64) api.FriendRequest { return api.FriendRequest{User: u, CreatedAt: t} }),
		Outgoing: entries(list.Outgoing, func(u api.User, t int64) api.FriendRequest { return api.FriendRequest{User: u, CreatedAt: t} }),
	})
}

// lookupUser serves GET /api/friends/lookup?handle=: someone found by their
// exact handle.
func (s *Server) lookupUser(w http.ResponseWriter, r *http.Request, cur auth.Current) {
	u, rel, err := s.Friends.Lookup(r.Context(), cur.User.ID, r.URL.Query().Get("handle"))
	if err != nil {
		friendError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, api.UserRelation{User: view.User(u), Relation: string(rel)})
}

func (s *Server) sendFriendRequest(w http.ResponseWriter, r *http.Request, cur auth.Current) {
	var req api.SendFriendRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	u, rel, changed, err := s.Friends.Request(r.Context(), cur.User.ID, req.UserID)
	if err != nil {
		friendError(w, r, err)
		return
	}
	if changed {
		kind := "friendRequest"
		if rel == friends.Friends {
			kind = "friendAccepted"
		}
		s.Realtime.NotifyUsers(api.Notification{Type: "notification", Kind: kind, User: view.User(cur.User)}, u.ID)
		s.friendsChanged(cur.User.ID, u.ID)
	}
	writeJSON(w, http.StatusOK, api.UserRelation{User: view.User(u), Relation: string(rel)})
}

func (s *Server) acceptFriendRequest(w http.ResponseWriter, r *http.Request, cur auth.Current) {
	u, err := s.Friends.Accept(r.Context(), cur.User.ID, r.PathValue("id"))
	if err != nil {
		friendError(w, r, err)
		return
	}
	s.Realtime.NotifyUsers(api.Notification{Type: "notification", Kind: "friendAccepted", User: view.User(cur.User)}, u.ID)
	s.friendsChanged(cur.User.ID, u.ID)
	writeJSON(w, http.StatusOK, api.UserRelation{User: view.User(u), Relation: string(friends.Friends)})
}

// removeFriendRequest declines a request to the user or cancels theirs.
func (s *Server) removeFriendRequest(w http.ResponseWriter, r *http.Request, cur auth.Current) {
	if _, err := s.Friends.RemoveRequest(r.Context(), cur.User.ID, r.PathValue("id")); err != nil {
		friendError(w, r, err)
		return
	}
	s.friendsChanged(cur.User.ID, r.PathValue("id"))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) unfriend(w http.ResponseWriter, r *http.Request, cur auth.Current) {
	if err := s.Friends.Unfriend(r.Context(), cur.User.ID, r.PathValue("id")); err != nil {
		friendError(w, r, err)
		return
	}
	s.friendsChanged(cur.User.ID, r.PathValue("id"))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listBlocked(w http.ResponseWriter, r *http.Request, cur auth.Current) {
	users, err := s.Friends.Blocked(r.Context(), cur.User.ID)
	if err != nil {
		internalError(w, r, err)
		return
	}
	out := []api.User{}
	for _, u := range users {
		out = append(out, view.User(u))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) block(w http.ResponseWriter, r *http.Request, cur auth.Current) {
	u, ended, err := s.Friends.Block(r.Context(), cur.User.ID, r.PathValue("id"))
	if err != nil {
		friendError(w, r, err)
		return
	}
	if ended {
		s.friendsChanged(cur.User.ID, u.ID)
	} else {
		s.friendsChanged(cur.User.ID)
	}
	writeJSON(w, http.StatusOK, api.UserRelation{User: view.User(u), Relation: string(friends.Blocked)})
}

func (s *Server) unblock(w http.ResponseWriter, r *http.Request, cur auth.Current) {
	ok, err := s.Friends.Unblock(r.Context(), cur.User.ID, r.PathValue("id"))
	switch {
	case err != nil:
		internalError(w, r, err)
	case !ok:
		writeError(w, http.StatusNotFound, "you have not blocked this person")
	default:
		s.friendsChanged(cur.User.ID)
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) listSuggestions(w http.ResponseWriter, r *http.Request, cur auth.Current) {
	list, err := s.Friends.Suggestions(r.Context(), cur.User.ID)
	if err != nil {
		internalError(w, r, err)
		return
	}
	out := []api.FriendSuggestion{}
	for _, sg := range list {
		guilds := []api.SharedGuild{}
		for _, g := range sg.Guilds {
			guilds = append(guilds, api.SharedGuild{ID: g.ID, Name: g.Name, IconURL: g.IconURL})
		}
		out = append(out, api.FriendSuggestion{User: view.User(sg.User), Guilds: guilds})
	}
	writeJSON(w, http.StatusOK, out)
}

// dismissSuggestion serves DELETE /api/friends/suggestions/{id}: that person
// is not suggested again.
func (s *Server) dismissSuggestion(w http.ResponseWriter, r *http.Request, cur auth.Current) {
	if err := s.Friends.Dismiss(r.Context(), cur.User.ID, r.PathValue("id")); err != nil {
		friendError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// friendsLive serves GET /api/friends/live: friends' streams in rooms the
// user may enter.
func (s *Server) friendsLive(w http.ResponseWriter, r *http.Request, cur auth.Current) {
	ctx := r.Context()
	ids, err := s.Friends.FriendIDs(ctx, cur.User.ID)
	if err != nil {
		internalError(w, r, err)
		return
	}
	friend := map[string]bool{}
	for _, id := range ids {
		friend[id] = true
	}
	out := []api.LiveFriend{}
	users := view.Users{DB: s.DB}
	for _, ls := range s.Gateway.ActiveStreams() {
		if !friend[ls.Stream.UserID] {
			continue
		}
		room, err := s.DB.GetRoom(ctx, ls.Stream.RoomID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			internalError(w, r, err)
			return
		}
		reason, err := s.Rooms.Check(ctx, room, cur.User.ID, access.Watch)
		if err != nil {
			internalError(w, r, err)
			return
		}
		if reason != access.OK {
			continue
		}
		st, err := view.Stream(ctx, &users, ls)
		if err != nil {
			internalError(w, r, err)
			return
		}
		ref := api.RoomRef{Slug: room.Slug, Kind: room.Kind}
		if room.OwnerID.Valid {
			owner, err := users.Get(ctx, room.OwnerID.String)
			if err != nil {
				internalError(w, r, err)
				return
			}
			ref.Owner = &owner
		}
		out = append(out, api.LiveFriend{Stream: st, Room: ref})
	}
	writeJSON(w, http.StatusOK, out)
}
