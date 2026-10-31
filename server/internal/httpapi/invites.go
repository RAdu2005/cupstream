package httpapi

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/RAdu2005/cupstream/server/internal/api"
	"github.com/RAdu2005/cupstream/server/internal/auth"
	"github.com/RAdu2005/cupstream/server/internal/rooms"
	"github.com/RAdu2005/cupstream/server/internal/store"
	"github.com/RAdu2005/cupstream/server/internal/view"
)

func (s *Server) inviteRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/rooms/{slug}/invites", requireUser(s.listInvites))
	mux.Handle("POST /api/rooms/{slug}/invites", requireUser(s.createInvite))
	mux.Handle("DELETE /api/rooms/{slug}/invites/{id}", requireUser(s.revokeInvite))
	// Invite tokens travel in the body, so they stay out of request logs.
	mux.HandleFunc("POST /api/invites/preview", s.previewInvite)
	mux.Handle("POST /api/invites/redeem", requireUser(s.redeemInvite))
}

// The largest use limit an invite link may have.
const maxInviteUses = 1000

func inviteView(inv store.Invite) api.Invite {
	return api.Invite{
		ID: inv.ID, CanStream: inv.CanStream, ExpiresAt: inv.ExpiresAt.Int64, UntilStreamEnd: inv.UntilStreamEnd,
		MaxUses: inv.MaxUses.Int64, Uses: inv.Uses, CreatedAt: inv.CreatedAt,
	}
}

func (s *Server) listInvites(w http.ResponseWriter, r *http.Request, cur auth.Current) {
	room, ok := s.ownRoom(w, r, cur)
	if !ok {
		return
	}
	invites, err := s.Rooms.ActiveInvites(r.Context(), room)
	if err != nil {
		internalError(w, r, err)
		return
	}
	out := []api.Invite{}
	for _, inv := range invites {
		out = append(out, inviteView(inv))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createInvite(w http.ResponseWriter, r *http.Request, cur auth.Current) {
	room, ok := s.ownRoom(w, r, cur)
	if !ok {
		return
	}
	var req api.CreateInviteRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.MaxUses < 0 || req.MaxUses > maxInviteUses {
		writeError(w, http.StatusBadRequest, "the use limit is between 1 and 1000, or none")
		return
	}
	inv, token, err := s.Rooms.CreateInvite(r.Context(), room, cur.User.ID, rooms.NewInvite{
		Expiry: rooms.Expiry(req.Expiry), MaxUses: req.MaxUses, CanStream: req.CanStream,
	})
	if errors.Is(err, rooms.ErrInvalidExpiry) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		internalError(w, r, err)
		return
	}
	link := url.URL{Scheme: s.Config.PublicURL.Scheme, Host: s.Config.PublicURL.Host, Path: "/i/" + token}
	writeJSON(w, http.StatusCreated, api.NewInvite{Invite: inviteView(inv), URL: link.String()})
}

func (s *Server) revokeInvite(w http.ResponseWriter, r *http.Request, cur auth.Current) {
	room, ok := s.ownRoom(w, r, cur)
	if !ok {
		return
	}
	err := s.Rooms.RevokeInvite(r.Context(), room, r.PathValue("id"))
	if errors.Is(err, rooms.ErrNoInvite) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		internalError(w, r, err)
		return
	}
	s.accessChanged(r.Context(), room) // everyone who joined through it leaves
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) inviteError(w http.ResponseWriter, r *http.Request, err error) {
	var refused *rooms.RefusedError
	switch {
	case errors.Is(err, rooms.ErrInviteInvalid):
		writeErrorCode(w, http.StatusNotFound, "invite_invalid", err.Error())
	case errors.As(err, &refused):
		refusal(w, refused.Reason, refused.Error())
	default:
		internalError(w, r, err)
	}
}

// previewInvite serves POST /api/invites/preview, signed in or not.
func (s *Server) previewInvite(w http.ResponseWriter, r *http.Request) {
	var req api.InviteToken
	if !decodeJSON(w, r, &req) {
		return
	}
	inv, room, err := s.Rooms.InviteByToken(r.Context(), req.Token)
	if err != nil {
		s.inviteError(w, r, err)
		return
	}
	users := view.Users{DB: s.DB}
	owner, err := users.Get(r.Context(), room.OwnerID.String)
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, api.InvitePreview{RoomSlug: room.Slug, Owner: owner, CanStream: inv.CanStream})
}

func (s *Server) redeemInvite(w http.ResponseWriter, r *http.Request, cur auth.Current) {
	var req api.InviteToken
	if !decodeJSON(w, r, &req) {
		return
	}
	room, err := s.Rooms.Redeem(r.Context(), req.Token, cur.User.ID)
	if err != nil {
		s.inviteError(w, r, err)
		return
	}
	// A held session of theirs may resume, and open pages see the change.
	s.accessChanged(r.Context(), room)
	writeJSON(w, http.StatusOK, api.RedeemedInvite{RoomSlug: room.Slug})
}
