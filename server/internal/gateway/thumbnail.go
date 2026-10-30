package gateway

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	_ "image/jpeg" // thumbnails are JPEG
	"io"
	"net/http"
	"strconv"
	"time"
)

// Thumbnails: app and web senders upload a small JPEG of their stream every
// few seconds. It is kept in memory with the live stream, never stored, and
// goes when the stream ends. OBS streams have none.
const (
	MaxThumbnailBytes = 256 << 10
	// The largest width and height accepted, in pixels.
	maxThumbnailSide = 1280
	// Uploads closer together than this are refused.
	thumbnailMinInterval = time.Second
)

// Thumbnail is a stream's latest picture.
type Thumbnail struct {
	JPEG []byte
	At   time.Time
	ETag string // quoted, for If-None-Match
}

var (
	errThumbnailFormat  = errors.New("the thumbnail must be a JPEG of at most 1280x1280 pixels")
	errThumbnailTooSoon = errors.New("thumbnails may be sent at most once a second")
)

// checkThumbnail accepts JPEGs within the size limits.
func checkThumbnail(b []byte) error {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil || format != "jpeg" || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > maxThumbnailSide || cfg.Height > maxThumbnailSide {
		return errThumbnailFormat
	}
	return nil
}

// SetThumbnail replaces an active stream's thumbnail.
func (g *Gateway) SetThumbnail(streamID string, jpeg []byte) error {
	if err := checkThumbnail(jpeg); err != nil {
		return err
	}
	sum := sha256.Sum256(jpeg)
	now := g.now()
	g.mu.Lock()
	defer g.mu.Unlock()
	s, ok := g.streams[streamID]
	if !ok {
		return errStreamGone
	}
	if s.thumb != nil && now.Sub(s.thumb.At) < thumbnailMinInterval {
		return errThumbnailTooSoon
	}
	s.thumb = &Thumbnail{JPEG: jpeg, At: now, ETag: `"` + hex.EncodeToString(sum[:8]) + `"`}
	return nil
}

// Thumbnail returns an active stream's latest thumbnail.
func (g *Gateway) Thumbnail(streamID string) (Thumbnail, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	s, ok := g.streams[streamID]
	if !ok || s.thumb == nil {
		return Thumbnail{}, false
	}
	return *s.thumb, true
}

// HandleThumbnailUpload serves PUT /api/streams/{id}/thumbnail: the sender
// sends a JPEG with the stream's publish token as the bearer token.
func (g *Gateway) HandleThumbnailUpload(w http.ResponseWriter, r *http.Request) {
	ls, ok := g.Stream(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "", errStreamGone.Error())
		return
	}
	if !publishTokenValid(r, ls.Stream) {
		writeErr(w, http.StatusUnauthorized, "", "invalid publish token")
		return
	}
	if r.Header.Get("Content-Type") != "image/jpeg" {
		writeErr(w, http.StatusUnsupportedMediaType, "", errThumbnailFormat.Error())
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxThumbnailBytes+1))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "", "could not read the thumbnail")
		return
	}
	if len(body) > MaxThumbnailBytes {
		writeErr(w, http.StatusRequestEntityTooLarge, "", "the thumbnail is larger than 256 KiB")
		return
	}
	switch err := g.SetThumbnail(ls.Stream.ID, body); {
	case errors.Is(err, errStreamGone):
		writeErr(w, http.StatusNotFound, "", err.Error())
	case errors.Is(err, errThumbnailTooSoon):
		w.Header().Set("Retry-After", "1")
		writeErr(w, http.StatusTooManyRequests, "rate_limited", err.Error())
	case err != nil:
		writeErr(w, http.StatusUnprocessableEntity, "", err.Error())
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// HandleThumbnail serves GET /api/streams/{id}/thumbnail to userID ("" when
// signed out), who must be allowed to watch the stream.
func (g *Gateway) HandleThumbnail(w http.ResponseWriter, r *http.Request, userID string) {
	ls, ok := g.Stream(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "", errStreamGone.Error())
		return
	}
	if !g.allowWatch(w, r, ls.Stream, userID) {
		return
	}
	th, ok := g.Thumbnail(ls.Stream.ID)
	if !ok {
		writeErr(w, http.StatusNotFound, "", "this stream has no thumbnail yet")
		return
	}
	h := w.Header()
	// Access can end at any time, so shared caches keep nothing and browsers
	// check again every time.
	h.Set("Cache-Control", "private, no-cache")
	h.Set("ETag", th.ETag)
	if r.Header.Get("If-None-Match") == th.ETag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Type", "image/jpeg")
	h.Set("Content-Length", strconv.Itoa(len(th.JPEG)))
	w.WriteHeader(http.StatusOK)
	w.Write(th.JPEG)
}
