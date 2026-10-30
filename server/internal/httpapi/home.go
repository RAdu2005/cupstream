package httpapi

import (
	"cmp"
	"context"
	"database/sql"
	"net/http"
	"slices"
	"strings"

	"github.com/RAdu2005/cupstream/server/internal/access"
	"github.com/RAdu2005/cupstream/server/internal/api"
	"github.com/RAdu2005/cupstream/server/internal/auth"
	"github.com/RAdu2005/cupstream/server/internal/store"
	"github.com/RAdu2005/cupstream/server/internal/view"
)

// home serves GET /api/home: the user's rooms and their friends' live
// rooms. Every room passes the same watch check as entering it.
func (s *Server) home(w http.ResponseWriter, r *http.Request, cur auth.Current) {
	ctx := r.Context()
	users := view.Users{DB: s.DB}
	out := api.Home{Rooms: []api.HomeRoom{}, FriendsLive: []api.HomeRoom{}}
	seen := map[string]bool{}

	// add describes room for the user if they may watch there.
	add := func(list *[]api.HomeRoom, room store.Room, how string) error {
		if seen[room.ID] {
			return nil
		}
		hr, ok, err := s.homeRoom(ctx, &users, room, cur.User.ID)
		if err != nil || !ok {
			return err
		}
		seen[room.ID] = true
		hr.Access = how
		*list = append(*list, hr)
		return nil
	}

	own, err := s.DB.GetLinkRoomByOwner(ctx, sql.NullString{String: cur.User.ID, Valid: true})
	if err != nil {
		internalError(w, r, err)
		return
	}
	if err := add(&out.Rooms, own, "own"); err != nil {
		internalError(w, r, err)
		return
	}
	allowed, err := s.DB.ListAllowedRooms(ctx, cur.User.ID)
	if err != nil {
		internalError(w, r, err)
		return
	}
	passes, err := s.DB.ListPassRooms(ctx, store.ListPassRoomsParams{UserID: cur.User.ID, Now: timeNow().Unix()})
	if err != nil {
		internalError(w, r, err)
		return
	}
	for _, row := range allowed {
		if err := add(&out.Rooms, row.Room, "allowed"); err != nil {
			internalError(w, r, err)
			return
		}
	}
	for _, row := range passes {
		if err := add(&out.Rooms, row.Room, "pass"); err != nil {
			internalError(w, r, err)
			return
		}
	}
	// Live rooms first, then by owner; the user's own room stays first.
	slices.SortStableFunc(out.Rooms[1:], func(a, b api.HomeRoom) int {
		return cmp.Or(cmp.Compare(len(b.Streams), len(a.Streams)), cmp.Compare(ownerName(a), ownerName(b)))
	})

	voice, ok, err := s.Rooms.CurrentDiscordRoom(ctx, cur.User.ID)
	if err != nil {
		internalError(w, r, err)
		return
	}
	if ok {
		var list []api.HomeRoom
		if err := add(&list, voice, ""); err != nil {
			internalError(w, r, err)
			return
		}
		if len(list) == 1 {
			out.Voice = &list[0]
		}
	}

	friendIDs, err := s.Friends.FriendIDs(ctx, cur.User.ID)
	if err != nil {
		internalError(w, r, err)
		return
	}
	for _, id := range friendIDs {
		ls, ok := s.Gateway.UserStream(id)
		if !ok {
			continue
		}
		room, err := s.DB.GetRoom(ctx, ls.Stream.RoomID)
		if err == nil {
			err = add(&out.FriendsLive, room, "")
		}
		if err != nil {
			internalError(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func ownerName(r api.HomeRoom) string {
	if r.Owner == nil {
		return ""
	}
	return strings.ToLower(r.Owner.DisplayName)
}

// homeRoom describes room for userID, and reports false if they may not
// watch there.
func (s *Server) homeRoom(ctx context.Context, users *view.Users, room store.Room, userID string) (api.HomeRoom, bool, error) {
	perms, err := s.Rooms.Permissions(ctx, room, userID)
	if err != nil || perms.Watch != access.OK {
		return api.HomeRoom{}, false, err
	}
	hr := api.HomeRoom{Slug: room.Slug, Kind: room.Kind, CanStream: perms.Stream == access.OK, Streams: []api.Stream{}}
	if room.OwnerID.Valid {
		owner, err := users.Get(ctx, room.OwnerID.String)
		if err != nil {
			return hr, false, err
		}
		hr.Owner = &owner
	}
	if room.Kind == string(access.Discord) {
		// Watching needs presence in the channel, so names are not a secret.
		if hr.Discord, err = s.discordChannel(ctx, room, userID, true); err != nil {
			return hr, false, err
		}
	}
	for _, ls := range s.Gateway.RoomStreams(room.ID) {
		st, err := view.Stream(ctx, users, ls)
		if err != nil {
			return hr, false, err
		}
		hr.Streams = append(hr.Streams, st)
	}
	return hr, true, nil
}
