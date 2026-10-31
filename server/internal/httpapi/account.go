package httpapi

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/RAdu2005/cupstream/server/internal/api"
	"github.com/RAdu2005/cupstream/server/internal/auth"
	"github.com/RAdu2005/cupstream/server/internal/store"
	"github.com/RAdu2005/cupstream/server/internal/view"
)

func (s *Server) accountRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/account", requireUser(s.account))
	mux.Handle("PATCH /api/account", requireUser(s.updateProfile))
	mux.Handle("PATCH /api/account/settings", requireUser(s.updateSettings))
	mux.Handle("DELETE /api/account/identities/{provider}", requireUser(s.unlink))
	mux.Handle("GET /api/account/sessions", requireUser(s.listSessions))
	mux.Handle("DELETE /api/account/sessions/{id}", requireUser(s.revokeSession))
	mux.Handle("GET /api/account/guilds", requireUser(s.listGuilds))
	mux.Handle("PATCH /api/account/guilds/{id}", requireUser(s.updateGuild))
	mux.Handle("GET /api/account/merge", requireUser(s.pendingMerge))
	mux.Handle("POST /api/account/merge", requireUser(s.confirmMerge))
	mux.Handle("DELETE /api/account/merge", requireUser(s.cancelMerge))
	mux.Handle("GET /api/account/stream-key", requireUser(s.streamKey))
	mux.Handle("POST /api/account/stream-key", requireUser(s.regenerateStreamKey))
}

func (s *Server) account(w http.ResponseWriter, r *http.Request, cur auth.Current) {
	ctx := r.Context()
	ids, err := s.DB.ListIdentities(ctx, cur.User.ID)
	if err != nil {
		internalError(w, r, err)
		return
	}
	settings, err := s.DB.UserSettings(ctx, cur.User.ID)
	if err != nil {
		internalError(w, r, err)
		return
	}
	room, err := s.DB.GetLinkRoomByOwner(ctx, sql.NullString{String: cur.User.ID, Valid: true})
	if err != nil {
		internalError(w, r, err)
		return
	}
	out := api.Account{
		User:       view.User(cur.User),
		Identities: []api.Identity{},
		Settings:   api.Settings{StartMode: settings.StartMode, ObsFallback: settings.ObsFallback},
		RoomSlug:   room.Slug,
	}
	for _, id := range ids {
		out.Identities = append(out.Identities, api.Identity{Provider: id.Provider, Username: id.Username})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) updateProfile(w http.ResponseWriter, r *http.Request, cur auth.Current) {
	var req api.UpdateProfileRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	u, err := s.Accounts.UpdateProfile(r.Context(), cur.User.ID, req.Handle, req.DisplayName)
	switch {
	case errors.Is(err, auth.ErrHandleTaken):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, auth.ErrInvalidHandle), errors.Is(err, auth.ErrInvalidName):
		writeError(w, http.StatusBadRequest, err.Error())
	case err != nil:
		internalError(w, r, err)
	default:
		s.Realtime.UserChanged(view.User(u))
		writeJSON(w, http.StatusOK, api.Me{User: view.User(u)})
	}
}

func (s *Server) updateSettings(w http.ResponseWriter, r *http.Request, cur auth.Current) {
	var req api.Settings
	if !decodeJSON(w, r, &req) {
		return
	}
	if (req.StartMode != "discord_auto" && req.StartMode != "link") || (req.ObsFallback != "link" && req.ObsFallback != "refuse") {
		writeError(w, http.StatusBadRequest, "invalid settings")
		return
	}
	if err := s.DB.UpdateUserSettings(r.Context(), store.UpdateUserSettingsParams{
		StartMode: req.StartMode, ObsFallback: req.ObsFallback, UserID: cur.User.ID,
	}); err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, req)
}

func (s *Server) unlink(w http.ResponseWriter, r *http.Request, cur auth.Current) {
	err := s.Accounts.Unlink(r.Context(), cur.User.ID, r.PathValue("provider"))
	switch {
	case errors.Is(err, auth.ErrLastIdentity):
		writeError(w, http.StatusConflict, err.Error())
	case err != nil:
		internalError(w, r, err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request, cur auth.Current) {
	list, err := s.Sessions.List(r.Context(), cur.User.ID)
	if err != nil {
		internalError(w, r, err)
		return
	}
	out := []api.Session{}
	for _, se := range list {
		out = append(out, api.Session{
			ID: se.ID, Kind: se.Kind, DeviceName: se.DeviceName,
			CreatedAt: se.CreatedAt, LastSeenAt: se.LastSeenAt, Current: se.ID == cur.SessionID,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) revokeSession(w http.ResponseWriter, r *http.Request, cur auth.Current) {
	ok, err := s.Sessions.Revoke(r.Context(), cur.User.ID, r.PathValue("id"))
	switch {
	case err != nil:
		internalError(w, r, err)
	case !ok:
		writeError(w, http.StatusNotFound, "no such session")
	default:
		s.Realtime.CloseSession(r.PathValue("id"))
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) listGuilds(w http.ResponseWriter, r *http.Request, cur auth.Current) {
	gs, err := s.DB.ListUserGuilds(r.Context(), cur.User.ID)
	if err != nil {
		internalError(w, r, err)
		return
	}
	out := []api.Guild{}
	for _, g := range gs {
		out = append(out, api.Guild{
			ID: g.GuildID, Name: g.Name, IconURL: g.Icon, ApproxMembers: g.ApproxMembers, SuggestionsEnabled: g.SuggestionsEnabled,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) updateGuild(w http.ResponseWriter, r *http.Request, cur auth.Current) {
	var req api.UpdateGuildRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	n, err := s.DB.SetGuildSuggestions(r.Context(), store.SetGuildSuggestionsParams{
		SuggestionsEnabled: req.SuggestionsEnabled, UserID: cur.User.ID, GuildID: r.PathValue("id"),
	})
	switch {
	case err != nil:
		internalError(w, r, err)
	case n == 0:
		writeError(w, http.StatusNotFound, "no such server")
	default:
		writeJSON(w, http.StatusOK, req)
	}
}

func (s *Server) whipURL(r *http.Request) string {
	return s.Config.PublicURL.JoinPath("whip").String()
}

func (s *Server) streamKey(w http.ResponseWriter, r *http.Request, cur auth.Current) {
	out := api.StreamKeyInfo{WhipURL: s.whipURL(r)}
	k, err := s.DB.GetActiveStreamKey(r.Context(), cur.User.ID)
	switch {
	case err == nil:
		out.HasKey, out.CreatedAt = true, k.CreatedAt
	case !errors.Is(err, sql.ErrNoRows):
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// regenerateStreamKey revokes the current OBS stream key and returns a new
// one. The key is shown once; only its hash is stored.
func (s *Server) regenerateStreamKey(w http.ResponseWriter, r *http.Request, cur auth.Current) {
	token, _ := auth.NewToken()
	key := "cs_" + token
	now := timeNow().Unix()
	err := s.DB.Tx(r.Context(), func(q *store.Queries) error {
		if err := q.RevokeStreamKeys(r.Context(), store.RevokeStreamKeysParams{
			RevokedAt: sql.NullInt64{Int64: now, Valid: true}, UserID: cur.User.ID,
		}); err != nil {
			return err
		}
		return q.CreateStreamKey(r.Context(), store.CreateStreamKeyParams{
			ID: uuid.Must(uuid.NewV7()).String(), UserID: cur.User.ID, KeyHash: auth.HashToken(key), CreatedAt: now,
		})
	})
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, api.NewStreamKey{Key: key, WhipURL: s.whipURL(r)})
}
