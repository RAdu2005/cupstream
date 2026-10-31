// Command cupstream runs the CupStream server.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/RAdu2005/cupstream/server/internal/auth"
	"github.com/RAdu2005/cupstream/server/internal/capacity"
	"github.com/RAdu2005/cupstream/server/internal/config"
	"github.com/RAdu2005/cupstream/server/internal/friends"
	"github.com/RAdu2005/cupstream/server/internal/gateway"
	"github.com/RAdu2005/cupstream/server/internal/httpapi"
	"github.com/RAdu2005/cupstream/server/internal/mediamtx"
	"github.com/RAdu2005/cupstream/server/internal/oauth"
	"github.com/RAdu2005/cupstream/server/internal/realtime"
	"github.com/RAdu2005/cupstream/server/internal/rooms"
	"github.com/RAdu2005/cupstream/server/internal/seal"
	"github.com/RAdu2005/cupstream/server/internal/store"
	"github.com/RAdu2005/cupstream/server/internal/webui"
)

// version is the commit the binary was built from, set with
// -ldflags "-X main.version=..." (see the Makefile).
var version = "dev"

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}
	setupLogging(cfg)
	slog.Info("starting", "version", version)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := store.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()

	sealer, err := seal.New(cfg.SecretKey)
	if err != nil {
		return err
	}
	if cfg.SecretKeyGenerated {
		slog.Warn("CUPSTREAM_SECRET_KEY is not set; using a random key, so stored Discord tokens are lost on restart")
	}

	sessions := auth.NewSessions(db, cfg.SecureCookies())
	go sessions.CleanupLoop(ctx)

	providers := map[string]oauth.Provider{}
	callback := func(p string) string { return cfg.PublicURL.JoinPath("api/auth", p, "callback").String() }
	if cfg.Google.Enabled() {
		providers[oauth.Google] = oauth.NewGoogle(oauth.GoogleIssuer, cfg.Google.ID, cfg.Google.Secret, callback(oauth.Google))
	}
	var discord *auth.DiscordSync
	if cfg.Discord.Enabled() {
		p := oauth.NewDiscord(oauth.DiscordWeb, oauth.DiscordAPI, cfg.Discord.ID, cfg.Discord.Secret, callback(oauth.Discord))
		providers[oauth.Discord] = p
		discord = auth.NewDiscordSync(db, sealer, p)
		go discord.SyncLoop(ctx)
	}
	if len(providers) == 0 && !cfg.DevLogin {
		slog.Warn("no sign-in provider is configured: set CUPSTREAM_GOOGLE_* or CUPSTREAM_DISCORD_*")
	}

	media := mediamtx.New(cfg.MediaMTXAPI, cfg.MediaMTXWebRTC)
	if info, err := media.Info(ctx); err != nil {
		slog.Warn("MediaMTX is not reachable yet", "api", cfg.MediaMTXAPI.String(), "err", err)
	} else {
		slog.Info("MediaMTX reachable", "version", info.Version)
	}

	roomService := rooms.New(db)
	gw := gateway.New(db, media, roomService, cfg.PublicURL)
	if cfg.EgressLimit > 0 {
		if meter, err := capacity.New(cfg.EgressInterfaces, cfg.EgressLimit); err != nil {
			slog.Warn("cannot measure outgoing traffic; viewers are not limited", "err", err)
		} else {
			slog.Info("refusing viewers above the bandwidth limit", "interfaces", meter.Usage().Interfaces, "limit_mbps", cfg.EgressLimit/1e6)
			go meter.Run(ctx)
			gw.Admission = meter
		}
	}
	hub := realtime.New(db, roomService, gw, cfg)
	gw.OnRoomIdle = hub.RoomIdle
	go func() {
		if err := gw.Run(ctx); err != nil {
			slog.Error("media gateway stopped", "err", err)
		}
	}()
	names := startDiscordBot(ctx, cfg, db, roomService, gw, hub)
	hubDone := make(chan struct{})
	go func() {
		defer close(hubDone)
		hub.Run(ctx)
	}()

	srv := &http.Server{
		Addr: cfg.Addr,
		Handler: httpapi.New(httpapi.Deps{
			Config:    cfg,
			Version:   version,
			DB:        db,
			Sealer:    sealer,
			Sessions:  sessions,
			Accounts:  auth.NewAccounts(db, discord),
			AppCodes:  auth.NewAppCodes(),
			Discord:   discord,
			Providers: providers,
			Rooms:     roomService,
			Gateway:   gw,
			Realtime:  hub,
			Friends:   friends.New(db),
			Bot:       names,
			MediaMTX:  media,
			Web:       webui.Handler(),
		}).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	if cfg.DevLogin {
		slog.Warn("development login is enabled")
	}

	errc := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", cfg.Addr, "tls", cfg.TLSCert != "", "public_url", cfg.PublicURL.String())
		if cfg.TLSCert != "" {
			errc <- srv.ListenAndServeTLS(cfg.TLSCert, cfg.TLSKey)
		} else {
			errc <- srv.ListenAndServe()
		}
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err = srv.Shutdown(shutdownCtx)
	<-hubDone // WebSockets are not part of the server's shutdown
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return nil
}

func setupLogging(cfg *config.Config) {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}
	var h slog.Handler = slog.NewJSONHandler(os.Stderr, opts)
	if cfg.LogFormat == "text" {
		h = slog.NewTextHandler(os.Stderr, opts)
	}
	slog.SetDefault(slog.New(h))
}
