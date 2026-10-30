//! The CupStream desktop app: a tray app that goes live in one click,
//! with audio that leaves out Discord (overview 6.2). The media pipeline is
//! cupstream-media's; this crate adds the window, tray, sign-in, the
//! realtime connection, the global hotkey and cupstream:// links.

mod keychain;
mod realtime;
mod settings;

use cupstream_client::{Account, ApiError, Client, Destination, SignIn, StartOptions, User};
use cupstream_media::capture::{self, VideoSource};
use cupstream_media::encoder::{self, Candidate};
use cupstream_media::{audio, gst, publish};
use serde::Serialize;
use settings::Settings;
use std::path::PathBuf;
use std::sync::{Arc, Mutex};
use std::time::Duration;
use tauri::menu::{Menu, MenuItem};
use tauri::tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent};
use tauri::{AppHandle, Emitter, Manager, State, WindowEvent};
use tauri_plugin_clipboard_manager::ClipboardExt;
use tauri_plugin_deep_link::DeepLinkExt;
use tauri_plugin_global_shortcut::{GlobalShortcutExt, ShortcutState};
use tauri_plugin_opener::OpenerExt;
use tokio::sync::mpsc;

/// Opus's share of the stream's bitrate, left out of the server's target.
const AUDIO_KBPS: u32 = 128;
/// The lowest video bitrate the server's target can push the encoder to.
const MIN_VIDEO_KBPS: u32 = 500;

/// What the window shows. Sent as the "status" event whenever it changes.
#[derive(Clone, Default, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Status {
    pub user: Option<User>,
    /// "idle", "signingIn", "starting", "live" or "reconnecting".
    pub phase: &'static str,
    pub room_url: Option<String>,
    pub encoder: Option<String>,
    /// The video bitrate in use, in kbit/s, and whether the server's
    /// target is lowering it.
    pub bitrate_kbps: Option<u32>,
    pub limited: bool,
    pub warnings: Vec<String>,
    pub audio: Vec<AudioStream>,
    pub error: Option<String>,
    /// Why the hotkey does not work, if it does not.
    pub hotkey_error: Option<String>,
}

#[derive(Clone, Serialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct AudioStream {
    pub name: String,
    pub captured: bool,
}

/// An error for the window: a machine-readable code it can act on
/// ("already_live" asks to end the other stream, "discord_unavailable"
/// offers the link room) and a message.
#[derive(Debug, Serialize)]
pub struct CmdError {
    code: String,
    message: String,
}

impl From<anyhow::Error> for CmdError {
    fn from(e: anyhow::Error) -> Self {
        match e.downcast_ref::<ApiError>() {
            Some(api) => CmdError {
                code: api.code.clone(),
                message: api.error.clone(),
            },
            None => CmdError {
                code: "error".into(),
                message: format!("{e:#}"),
            },
        }
    }
}

type CmdResult<T> = Result<T, CmdError>;

struct Live {
    publisher: publish::Publisher,
    dest: Destination,
    client: Client,
    /// The bitrate the user chose; the server's target only lowers it.
    bitrate: u32,
    generation: u64,
}

/// What going live opens before it publishes.
struct Prepared {
    encoder: &'static Candidate,
    capture: capture::Capture,
    audio: Option<audio::AudioCapture>,
}

fn publish(
    p: Prepared,
    settings: &Settings,
    dest: &Destination,
) -> anyhow::Result<publish::Publisher> {
    let video = publish::VideoSettings {
        bitrate_kbps: settings.bitrate(),
        fps: settings.preset.fps(),
        height: settings.preset.height(),
        keyframe_secs: 4,
    };
    let endpoint = publish::Endpoint {
        url: dest.whip_url.clone(),
        token: dest.token.clone(),
    };
    let publisher = publish::Publisher::new(p.capture, p.audio, p.encoder, video, &endpoint)?;
    publisher.start()?;
    Ok(publisher)
}

pub struct Core {
    app: AppHandle,
    settings_path: PathBuf,
    settings: Mutex<Settings>,
    session: Mutex<Option<(String, User)>>,
    live: Mutex<Option<Live>>,
    status: Mutex<Status>,
    realtime: Mutex<Option<realtime::Realtime>>,
    events: mpsc::UnboundedSender<realtime::Event>,
    /// Going live and stopping run one at a time.
    op: tokio::sync::Mutex<u64>,
    /// The chosen encoder, by codec and setting, so it is probed once.
    encoder: Mutex<Option<(String, &'static Candidate)>>,
    toggle_item: Mutex<Option<MenuItem<tauri::Wry>>>,
}

fn device_name() -> String {
    let host = std::env::var("COMPUTERNAME")
        .ok()
        .or_else(|| {
            std::fs::read_to_string("/etc/hostname")
                .ok()
                .map(|s| s.trim().to_owned())
        })
        .filter(|h| !h.is_empty())
        .unwrap_or_else(|| "this computer".into());
    format!("CupStream app on {host}")
}

impl Core {
    fn settings(&self) -> Settings {
        self.settings.lock().unwrap().clone()
    }

    fn update_status(&self, f: impl FnOnce(&mut Status)) {
        let status = {
            let mut s = self.status.lock().unwrap();
            f(&mut s);
            s.clone()
        };
        if let Some(item) = self.toggle_item.lock().unwrap().as_ref() {
            let _ = item.set_text(if matches!(status.phase, "live" | "reconnecting") {
                "Stop live"
            } else {
                "Go live"
            });
            let _ = item.set_enabled(status.user.is_some() && status.phase != "starting");
        }
        let _ = self.app.emit("status", status);
    }

    fn client(&self) -> anyhow::Result<Client> {
        let token = self
            .session
            .lock()
            .unwrap()
            .as_ref()
            .map(|(t, _)| t.clone());
        Client::new(&self.settings().server, token)
    }

    fn show_window(&self) {
        if let Some(w) = self.app.get_webview_window("main") {
            let _ = w.show();
            let _ = w.unminimize();
            let _ = w.set_focus();
        }
    }

    /// Something outside the window (the website, a cupstream:// link, the
    /// hotkey) asks to go live: the window does it, because it may need to
    /// ask the user something first.
    fn request_go_live(&self, room: Option<String>) {
        self.show_window();
        let _ = self.app.emit("go-live-request", room);
    }

    /// Signs in with a token: checks it, starts the realtime connection.
    async fn activate(&self, token: String) -> anyhow::Result<()> {
        let server = self.settings().server;
        let user = Client::new(&server, Some(token.clone()))?.me().await?;
        *self.session.lock().unwrap() = Some((token.clone(), user.clone()));
        let rt = realtime::Realtime::start(&url::Url::parse(&server)?, token, self.events.clone());
        *self.realtime.lock().unwrap() = Some(rt);
        self.update_status(|s| {
            s.user = Some(user);
            s.error = None;
        });
        Ok(())
    }

    async fn restore_session(&self) {
        let server = self.settings().server;
        let s = server.clone();
        let Ok(Some(token)) = tokio::task::spawn_blocking(move || keychain::load(&s)).await else {
            self.show_window();
            return;
        };
        if let Err(e) = self.activate(token).await {
            tracing::warn!("restoring the session failed: {e:#}");
            if e.downcast_ref::<ApiError>()
                .is_some_and(|a| a.status == 401)
            {
                let _ = tokio::task::spawn_blocking(move || keychain::delete(&server)).await;
            }
            self.show_window();
        }
    }

    fn forget_session(&self) {
        *self.realtime.lock().unwrap() = None;
        *self.session.lock().unwrap() = None;
        let server = self.settings().server;
        std::thread::spawn(move || keychain::delete(&server));
        self.update_status(|s| {
            *s = Status {
                phase: "idle",
                hotkey_error: s.hotkey_error.clone(),
                ..Default::default()
            }
        });
    }

    fn choose_encoder(&self, settings: &Settings) -> anyhow::Result<&'static Candidate> {
        let key = format!("{:?}/{:?}", settings.codec(), settings.encoder);
        if let Some((k, c)) = self.encoder.lock().unwrap().as_ref()
            && *k == key
        {
            return Ok(c);
        }
        let (c, skipped) = encoder::choose(settings.codec(), settings.encoder.as_deref())?;
        for s in skipped {
            tracing::info!("skipped encoder {s}");
        }
        *self.encoder.lock().unwrap() = Some((key, c));
        Ok(c)
    }

    fn video_source(&self, settings: &Settings) -> VideoSource {
        let default = capture::default_source();
        #[cfg(target_os = "linux")]
        if let VideoSource::Portal { .. } = default {
            return VideoSource::Portal {
                restore_token: settings.restore_token.clone(),
            };
        }
        #[cfg(windows)]
        if let Some(src) = &settings.source {
            if let Some(h) = src.strip_prefix("window:").and_then(|h| h.parse().ok()) {
                return VideoSource::Window(h);
            }
            if let Some(n) = src.strip_prefix("monitor:").and_then(|n| n.parse().ok()) {
                return VideoSource::Monitor(n);
            }
        }
        let _ = settings;
        default
    }

    async fn go_live(self: &Arc<Self>, opts: StartOptions) -> anyhow::Result<()> {
        let mut generation = self.op.lock().await;
        if self.live.lock().unwrap().is_some() {
            return Ok(());
        }
        let client = self.client()?;
        let settings = self.settings();
        self.update_status(|s| {
            s.phase = "starting";
            s.error = None;
            s.warnings.clear();
        });
        let result = self.start(&client, &settings, opts).await;
        match result {
            Ok((publisher, dest, warnings)) => {
                *generation += 1;
                let room_url = dest
                    .room_slug
                    .as_deref()
                    .map(|r| client.room_url(r).to_string());
                if let Some(url) = &room_url {
                    let _ = self.app.clipboard().write_text(url.clone());
                }
                if let Some(rt) = self.realtime.lock().unwrap().as_ref() {
                    rt.follow(dest.room_slug.clone());
                }
                let encoder = format!(
                    "{} ({})",
                    publisher.encoder_info.label, publisher.encoder_info.factory
                );
                *self.live.lock().unwrap() = Some(Live {
                    publisher,
                    dest,
                    client,
                    bitrate: settings.bitrate(),
                    generation: *generation,
                });
                self.update_status(|s| {
                    s.phase = "live";
                    s.room_url = room_url;
                    s.encoder = Some(encoder);
                    s.bitrate_kbps = Some(settings.bitrate());
                    s.limited = false;
                    s.warnings = warnings;
                });
                tokio::spawn(watch(self.clone(), *generation));
                Ok(())
            }
            Err(e) => {
                self.update_status(|s| s.phase = "idle");
                Err(e)
            }
        }
    }

    async fn start(
        self: &Arc<Self>,
        client: &Client,
        settings: &Settings,
        opts: StartOptions,
    ) -> anyhow::Result<(publish::Publisher, Destination, Vec<String>)> {
        let (prepared, warnings) = self.prepare(settings).await?;
        // The stream is created last, so a cancelled screen choice does not
        // leave one behind.
        let dest = client.start_stream(&opts).await?;
        match publish(prepared, settings, &dest) {
            Ok(p) => Ok((p, dest, warnings)),
            Err(e) => {
                if let Some(id) = &dest.stream_id {
                    let _ = client.end_stream(id).await;
                }
                Err(e)
            }
        }
    }

    /// Picks the encoder and opens the capture (the portal may ask what to
    /// share) and the audio.
    async fn prepare(
        self: &Arc<Self>,
        settings: &Settings,
    ) -> anyhow::Result<(Prepared, Vec<String>)> {
        let s = settings.clone();
        let this = self.clone();
        let encoder = tokio::task::spawn_blocking(move || this.choose_encoder(&s)).await??;
        let capture = capture::open(&self.video_source(settings), settings.preset.fps()).await?;
        if capture.restore_token.is_some() && capture.restore_token != settings.restore_token {
            let mut st = self.settings.lock().unwrap();
            st.restore_token = capture.restore_token.clone();
            let _ = st.save(&self.settings_path);
        }
        let mode = settings.audio_mode();
        let audio = tokio::task::spawn_blocking(move || audio::open(&mode)).await??;
        let warnings = audio
            .as_ref()
            .map(|a| a.warnings.clone())
            .unwrap_or_default();
        Ok((
            Prepared {
                encoder,
                capture,
                audio,
            },
            warnings,
        ))
    }

    /// After the connection dropped: publishes again to the same stream
    /// (its WHIP URL and token stay valid), a few times with backoff,
    /// while the user does not stop.
    async fn reconnect(self: &Arc<Self>, generation: u64, error: String) -> bool {
        tracing::warn!("the stream failed ({error}); reconnecting");
        let dest = {
            let live = self.live.lock().unwrap();
            let Some(live) = live.as_ref().filter(|l| l.generation == generation) else {
                return false;
            };
            live.publisher.stop();
            live.dest.clone()
        };
        self.update_status(|s| {
            s.phase = "reconnecting";
            s.error = Some(format!("Connection lost ({error}). Reconnecting…"));
        });
        for delay in [1, 2, 4, 8, 8] {
            tokio::time::sleep(Duration::from_secs(delay)).await;
            let settings = self.settings();
            let attempt =
                async { publish(self.prepare(&settings).await?.0, &settings, &dest) }.await;
            let mut guard = self.live.lock().unwrap();
            let Some(live) = guard.as_mut().filter(|l| l.generation == generation) else {
                // Stopped meanwhile.
                if let Ok(p) = attempt {
                    p.stop();
                }
                return false;
            };
            match attempt {
                Ok(p) => {
                    p.set_bitrate(
                        self.status
                            .lock()
                            .unwrap()
                            .bitrate_kbps
                            .unwrap_or(live.bitrate),
                    );
                    live.publisher = p;
                    drop(guard);
                    self.update_status(|s| {
                        s.phase = "live";
                        s.error = None;
                    });
                    return true;
                }
                Err(e) => tracing::warn!("reconnecting failed: {e:#}"),
            }
        }
        false
    }

    async fn stop(&self, error: Option<String>) {
        let _guard = self.op.lock().await;
        let Some(live) = self.live.lock().unwrap().take() else {
            return;
        };
        live.publisher.stop();
        if let Some(rt) = self.realtime.lock().unwrap().as_ref() {
            rt.follow(None);
        }
        // Stopping sends the WHIP DELETE, which ends the stream; this
        // covers a pipeline that failed without sending it.
        if let Some(id) = &live.dest.stream_id
            && let Err(e) = live.client.end_stream(id).await
            && e.downcast_ref::<ApiError>().is_none_or(|a| a.status != 404)
        {
            tracing::warn!("ending the stream failed: {e:#}");
        }
        self.update_status(|s| {
            s.phase = "idle";
            s.room_url = None;
            s.bitrate_kbps = None;
            s.limited = false;
            s.audio.clear();
            s.error = error;
        });
    }

    /// Applies the server's target: the stream's bitrate, minus audio, as
    /// a ceiling on the user's own choice.
    fn apply_target(&self, stream_id: &str, max_bitrate: u64) {
        let applied = {
            let live = self.live.lock().unwrap();
            let Some(live) = live
                .as_ref()
                .filter(|l| l.dest.stream_id.as_deref() == Some(stream_id))
            else {
                return;
            };
            let kbps = if max_bitrate == 0 {
                live.bitrate
            } else {
                let target = (max_bitrate / 1000).saturating_sub(AUDIO_KBPS as u64) as u32;
                live.bitrate.min(target.max(MIN_VIDEO_KBPS))
            };
            live.publisher.set_bitrate(kbps);
            (kbps, kbps < live.bitrate)
        };
        let changed = self.status.lock().unwrap().bitrate_kbps != Some(applied.0);
        if changed {
            tracing::info!(
                "video bitrate {} kbit/s (server target {max_bitrate} bit/s)",
                applied.0
            );
            self.update_status(|s| {
                s.bitrate_kbps = Some(applied.0);
                s.limited = applied.1;
            });
        }
    }

    fn register_hotkey(self: &Arc<Self>) {
        let gs = self.app.global_shortcut();
        let _ = gs.unregister_all();
        let hotkey = self.settings().hotkey;
        let error = if hotkey.trim().is_empty() {
            None
        } else {
            let core = self.clone();
            gs.on_shortcut(hotkey.as_str(), move |_, _, event| {
                if event.state != ShortcutState::Pressed {
                    return;
                }
                let core = core.clone();
                tauri::async_runtime::spawn(async move {
                    if core.live.lock().unwrap().is_some() {
                        core.stop(None).await;
                    } else {
                        core.request_go_live(None);
                    }
                });
            })
            .err()
            .map(|e| {
                #[cfg(target_os = "linux")]
                if capture::is_wayland() {
                    return "Global hotkeys are not available on Wayland yet.".to_owned();
                }
                format!("The hotkey {hotkey} could not be registered: {e}")
            })
        };
        self.update_status(|s| s.hotkey_error = error);
    }

    fn handle_link(&self, url: &url::Url) {
        if url.scheme() != "cupstream" {
            return;
        }
        // cupstream://go-live?room=<slug> goes live; anything else opens
        // the window.
        if url.host_str() == Some("go-live") {
            let room = url
                .query_pairs()
                .find(|(k, _)| k == "room")
                .map(|(_, v)| v.into_owned());
            self.request_go_live(room.filter(|r| !r.is_empty()));
        } else {
            self.show_window();
        }
    }
}

/// Follows a live pipeline: stops on errors and shows which applications
/// are heard.
async fn watch(core: Arc<Core>, generation: u64) {
    let mut tick = tokio::time::interval(Duration::from_millis(250));
    loop {
        tick.tick().await;
        let (error, audio) = {
            let live = core.live.lock().unwrap();
            let Some(live) = live.as_ref().filter(|l| l.generation == generation) else {
                return;
            };
            let bus = live.publisher.bus();
            let mut error = None;
            while let Some(msg) = bus.pop() {
                if let gst::MessageView::Error(e) = msg.view() {
                    error = Some(e.error().to_string());
                }
            }
            let audio: Vec<AudioStream> = live
                .publisher
                .audio()
                .map(|a| {
                    a.streams()
                        .into_iter()
                        .map(|s| AudioStream {
                            name: s.name,
                            captured: s.captured,
                        })
                        .collect()
                })
                .unwrap_or_default();
            (error, audio)
        };
        if let Some(e) = error {
            if !core.reconnect(generation, e.clone()).await {
                core.stop(Some(format!("The stream stopped: {e}"))).await;
                return;
            }
            continue;
        }
        if core.status.lock().unwrap().audio != audio {
            core.update_status(|s| s.audio = audio);
        }
    }
}

// --- Commands for the window ------------------------------------------

#[tauri::command]
fn status(core: State<'_, Arc<Core>>) -> Status {
    core.status.lock().unwrap().clone()
}

#[tauri::command]
async fn sign_in(core: State<'_, Arc<Core>>) -> CmdResult<()> {
    let server = core.settings().server;
    core.update_status(|s| s.phase = "signingIn");
    let result: anyhow::Result<()> = async {
        let flow = SignIn::start(&server, &device_name()).await?;
        core.app
            .opener()
            .open_url(flow.url.as_str(), None::<&str>)?;
        let signed_in = flow.finish().await?;
        let token = signed_in.token.clone();
        let s = server.clone();
        tokio::task::spawn_blocking(move || keychain::store(&s, &token)).await??;
        core.activate(signed_in.token).await
    }
    .await;
    core.update_status(|s| s.phase = "idle");
    core.show_window();
    Ok(result?)
}

#[tauri::command]
async fn sign_out(core: State<'_, Arc<Core>>) -> CmdResult<()> {
    core.stop(None).await;
    if let Err(e) = core.client()?.sign_out().await {
        tracing::warn!("signing out on the server failed: {e:#}");
    }
    core.forget_session();
    Ok(())
}

#[tauri::command]
async fn go_live(
    core: State<'_, Arc<Core>>,
    room: Option<String>,
    link_fallback: bool,
    replace: bool,
) -> CmdResult<()> {
    let core = core.inner().clone();
    Ok(core
        .go_live(StartOptions {
            room,
            link_fallback,
            replace,
        })
        .await?)
}

#[tauri::command]
async fn stop(core: State<'_, Arc<Core>>) -> CmdResult<()> {
    core.stop(None).await;
    Ok(())
}

#[tauri::command]
fn get_settings(core: State<'_, Arc<Core>>) -> Settings {
    core.settings()
}

#[tauri::command]
fn save_settings(core: State<'_, Arc<Core>>, settings: Settings) -> CmdResult<()> {
    let server_changed = core.settings().server != settings.server;
    settings.save(&core.settings_path).map_err(CmdError::from)?;
    *core.settings.lock().unwrap() = settings;
    core.register_hotkey();
    if server_changed {
        *core.realtime.lock().unwrap() = None;
        *core.session.lock().unwrap() = None;
        core.update_status(|s| s.user = None);
        let core = core.inner().clone();
        tauri::async_runtime::spawn(async move { core.restore_session().await });
    }
    Ok(())
}

/// One encoder and whether it works on this computer.
#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct EncoderView {
    factory: &'static str,
    label: &'static str,
    codec: &'static str,
    hardware: bool,
    error: Option<String>,
}

#[tauri::command]
async fn encoders() -> CmdResult<Vec<EncoderView>> {
    let list = tokio::task::spawn_blocking(encoder::report)
        .await
        .map_err(|e| CmdError::from(anyhow::Error::from(e)))?;
    Ok(list
        .into_iter()
        .filter(|(_, r)| r.as_ref().err().is_none_or(|e| e != "not installed"))
        .map(|(c, r)| EncoderView {
            factory: c.factory,
            label: c.label,
            codec: match c.codec {
                encoder::Codec::H264 => "h264",
                encoder::Codec::Av1 => "av1",
            },
            hardware: c.hardware,
            error: r.err(),
        })
        .collect())
}

#[tauri::command]
async fn account(core: State<'_, Arc<Core>>) -> CmdResult<Account> {
    Ok(core.client()?.account().await?)
}

#[tauri::command]
async fn set_start_mode(core: State<'_, Arc<Core>>, mode: String) -> CmdResult<()> {
    let client = core.client()?;
    let account = client.account().await?;
    Ok(client.set_start_mode(&account, &mode).await?)
}

#[tauri::command]
async fn set_visibility(core: State<'_, Arc<Core>>, visibility: String) -> CmdResult<()> {
    let client = core.client()?;
    let account = client.account().await?;
    Ok(client
        .set_visibility(&account.room_slug, &visibility)
        .await?)
}

#[tauri::command]
fn copy_link(core: State<'_, Arc<Core>>) -> CmdResult<()> {
    if let Some(url) = core.status.lock().unwrap().room_url.clone() {
        core.app
            .clipboard()
            .write_text(url)
            .map_err(|e| CmdError::from(anyhow::Error::from(e)))?;
    }
    Ok(())
}

#[tauri::command]
fn open_room(core: State<'_, Arc<Core>>) -> CmdResult<()> {
    if let Some(url) = core.status.lock().unwrap().room_url.clone() {
        core.app
            .opener()
            .open_url(url, None::<&str>)
            .map_err(|e| CmdError::from(anyhow::Error::from(e)))?;
    }
    Ok(())
}

#[tauri::command]
async fn quit(core: State<'_, Arc<Core>>) -> CmdResult<()> {
    core.stop(None).await;
    core.app.exit(0);
    Ok(())
}

// --- Setup -------------------------------------------------------------

fn build_tray(app: &tauri::App, core: &Arc<Core>) -> tauri::Result<()> {
    let toggle = MenuItem::with_id(app, "toggle", "Go live", false, None::<&str>)?;
    let copy = MenuItem::with_id(app, "copy", "Copy room link", true, None::<&str>)?;
    let open = MenuItem::with_id(app, "open", "Open CupStream", true, None::<&str>)?;
    let quit = MenuItem::with_id(app, "quit", "Quit", true, None::<&str>)?;
    let menu = Menu::with_items(app, &[&toggle, &copy, &open, &quit])?;
    *core.toggle_item.lock().unwrap() = Some(toggle);

    let c = core.clone();
    let mut tray = TrayIconBuilder::with_id("main")
        .tooltip("CupStream")
        .menu(&menu)
        .show_menu_on_left_click(false)
        .on_menu_event(move |_, event| {
            let core = c.clone();
            match event.id.as_ref() {
                "toggle" => {
                    tauri::async_runtime::spawn(async move {
                        if core.live.lock().unwrap().is_some() {
                            core.stop(None).await;
                        } else {
                            core.request_go_live(None);
                        }
                    });
                }
                "copy" => {
                    if let Some(url) = core.status.lock().unwrap().room_url.clone() {
                        let _ = core.app.clipboard().write_text(url);
                    }
                }
                "open" => core.show_window(),
                "quit" => {
                    tauri::async_runtime::spawn(async move {
                        core.stop(None).await;
                        core.app.exit(0);
                    });
                }
                _ => {}
            }
        })
        .on_tray_icon_event(|tray, event| {
            if let TrayIconEvent::Click {
                button: MouseButton::Left,
                button_state: MouseButtonState::Up,
                ..
            } = event
                && let Some(core) = tray.app_handle().try_state::<Arc<Core>>()
            {
                core.show_window();
            }
        });
    if let Some(icon) = app.default_window_icon() {
        tray = tray.icon(icon.clone());
    }
    tray.build(app)?;
    Ok(())
}

pub fn run() {
    tracing_subscriber::fmt()
        .with_env_filter(
            tracing_subscriber::EnvFilter::try_from_default_env().unwrap_or_else(|_| "info".into()),
        )
        .init();

    tauri::Builder::default()
        // A second launch (or a cupstream:// link) goes to the running app.
        .plugin(tauri_plugin_single_instance::init(|app, _, _| {
            if let Some(core) = app.try_state::<Arc<Core>>() {
                core.show_window();
            }
        }))
        .plugin(tauri_plugin_deep_link::init())
        .plugin(tauri_plugin_opener::init())
        .plugin(tauri_plugin_clipboard_manager::init())
        .plugin(tauri_plugin_global_shortcut::Builder::new().build())
        .setup(|app| {
            cupstream_media::init()?;
            let settings_path = app.path().app_config_dir()?.join("settings.json");
            let (events, mut events_rx) = mpsc::unbounded_channel();
            let core = Arc::new(Core {
                app: app.handle().clone(),
                settings: Mutex::new(Settings::load(&settings_path)),
                settings_path,
                session: Mutex::default(),
                live: Mutex::default(),
                status: Mutex::new(Status {
                    phase: "idle",
                    ..Default::default()
                }),
                realtime: Mutex::default(),
                events,
                op: tokio::sync::Mutex::new(0),
                encoder: Mutex::default(),
                toggle_item: Mutex::default(),
            });
            app.manage(core.clone());
            build_tray(app, &core)?;
            core.register_hotkey();

            // Development builds register the scheme themselves; installed
            // ones get it from the package.
            #[cfg(all(debug_assertions, any(target_os = "linux", windows)))]
            if let Err(e) = app.deep_link().register_all() {
                tracing::warn!("registering cupstream:// failed: {e}");
            }
            let c = core.clone();
            app.deep_link().on_open_url(move |event| {
                for url in event.urls() {
                    c.handle_link(&url);
                }
            });

            let c = core.clone();
            tauri::async_runtime::spawn(async move {
                c.restore_session().await;
                if let Ok(Some(urls)) = c.app.deep_link().get_current() {
                    for url in urls {
                        c.handle_link(&url);
                    }
                }
            });

            let c = core.clone();
            tauri::async_runtime::spawn(async move {
                while let Some(event) = events_rx.recv().await {
                    match event {
                        realtime::Event::GoLive { room } => c.request_go_live(room),
                        realtime::Event::BitrateTarget {
                            stream_id,
                            max_bitrate,
                        } => c.apply_target(&stream_id, max_bitrate),
                        realtime::Event::SignedOut => {
                            c.stop(None).await;
                            c.forget_session();
                            c.show_window();
                        }
                    }
                }
            });
            Ok(())
        })
        // Closing the window keeps the app in the tray.
        .on_window_event(|window, event| {
            if let WindowEvent::CloseRequested { api, .. } = event {
                api.prevent_close();
                let _ = window.hide();
            }
        })
        .invoke_handler(tauri::generate_handler![
            status,
            sign_in,
            sign_out,
            go_live,
            stop,
            get_settings,
            save_settings,
            encoders,
            account,
            set_start_mode,
            set_visibility,
            copy_link,
            open_room,
            quit,
        ])
        .run(tauri::generate_context!())
        .expect("error while running CupStream");
}
