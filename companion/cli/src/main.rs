//! cupstream-cli: the desktop app's pipeline from the command line, to
//! test capture, encoders, audio without Discord and publishing on each
//! platform before the app exists.

use anyhow::{Context, anyhow, bail};
use clap::{Parser, Subcommand, ValueEnum};
use cupstream_client::{Client, Destination, SignIn, StartOptions};
use cupstream_media::audio::{self, AudioMode};
use cupstream_media::capture::{self, VideoSource};
use cupstream_media::encoder::{self, Codec};
use cupstream_media::gst;
use cupstream_media::publish::{Endpoint, Publisher, VideoSettings};
use std::collections::BTreeMap;
use std::path::PathBuf;
use std::time::Duration;

#[derive(Parser)]
#[command(version, about = "CupStream desktop pipeline prototype")]
struct Cli {
    /// The CupStream server.
    #[arg(
        long,
        global = true,
        env = "CUPSTREAM_SERVER",
        default_value = "https://cupstream.larp.love"
    )]
    server: String,
    #[command(subcommand)]
    command: Command,
}

#[derive(Subcommand)]
enum Command {
    /// Sign in through the browser and remember the app session.
    SignIn,
    /// End the remembered app session.
    SignOut,
    /// List the encoders for this platform and whether they work here.
    Encoders,
    /// Show which applications' audio would be captured, until Ctrl+C.
    Audio {
        /// except-discord, all, or app:<name>.
        #[arg(long, default_value = "except-discord")]
        mode: String,
    },
    /// Capture, encode and publish until Ctrl+C.
    Publish(PublishArgs),
    /// Watch a room's stream over WHEP and report what was decoded.
    Watch {
        /// The room's slug.
        #[arg(long)]
        room: String,
        /// Seconds to watch.
        #[arg(long, default_value_t = 10)]
        seconds: u64,
        /// Also record what arrives into this directory: the audio as raw
        /// PCM and a frame per second, for quality checks.
        #[arg(long)]
        record: Option<PathBuf>,
    },
}

#[derive(clap::Args)]
struct PublishArgs {
    /// Publish with an OBS stream key to /whip instead of the signed-in
    /// app session.
    #[arg(long, env = "CUPSTREAM_STREAM_KEY")]
    stream_key: Option<String>,
    /// The room's slug; default: your start mode.
    #[arg(long)]
    room: Option<String>,
    /// End your stream elsewhere first.
    #[arg(long)]
    replace: bool,
    /// What to capture: portal, x11, x11:<xid>, monitor:<index>,
    /// window:<hwnd> or test. Default: portal on Wayland, x11 on X11,
    /// monitor:0 on Windows.
    #[arg(long)]
    source: Option<String>,
    /// Encoder element, e.g. vah264enc or x264enc; default: the best one
    /// that works.
    #[arg(long)]
    encoder: Option<String>,
    #[arg(long, value_enum, default_value_t = CodecArg::H264)]
    codec: CodecArg,
    /// Video bitrate in kbit/s.
    #[arg(long, default_value_t = 6000)]
    bitrate: u32,
    #[arg(long, default_value_t = 60)]
    fps: u32,
    /// Scale to this height (e.g. 1080); default: the source's size.
    #[arg(long)]
    height: Option<u32>,
    /// Seconds between keyframes when nobody asks for one.
    #[arg(long, default_value_t = 4)]
    keyframe_interval: u32,
    /// except-discord, all, app:<name>, test or none.
    #[arg(long, default_value = "except-discord")]
    audio: String,
    /// Stop after this many seconds.
    #[arg(long)]
    duration: Option<u64>,
    /// Ask for the screen again instead of reusing the last choice.
    #[arg(long)]
    pick: bool,
}

#[derive(Clone, Copy, ValueEnum)]
enum CodecArg {
    H264,
    Av1,
}

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    tracing_subscriber::fmt()
        .with_env_filter(
            tracing_subscriber::EnvFilter::try_from_default_env()
                .unwrap_or_else(|_| "info,cupstream_media=info".into()),
        )
        .with_writer(std::io::stderr)
        .init();
    let cli = Cli::parse();
    match cli.command {
        Command::SignIn => sign_in(&cli.server).await,
        Command::SignOut => sign_out(&cli.server).await,
        Command::Encoders => {
            cupstream_media::init()?;
            for (c, result) in encoder::report() {
                let hw = if c.hardware { "hardware" } else { "software" };
                match result {
                    Ok(()) => println!(
                        "ok       {:<16} {:?} {} ({hw})",
                        c.factory, c.codec, c.label
                    ),
                    Err(e) => println!(
                        "missing  {:<16} {:?} {} ({hw}): {e}",
                        c.factory, c.codec, c.label
                    ),
                }
            }
            Ok(())
        }
        Command::Audio { mode } => {
            cupstream_media::init()?;
            let capture = audio::open(&parse_audio(&mode)?)?.ok_or_else(|| anyhow!("no audio"))?;
            for w in &capture.warnings {
                eprintln!("warning: {w}");
            }
            show_audio_until_ctrl_c(&capture).await;
            Ok(())
        }
        Command::Publish(args) => publish(&cli.server, args).await,
        Command::Watch {
            room,
            seconds,
            record,
        } => {
            cupstream_media::init()?;
            let client = Client::new(&cli.server, load("tokens.json").get(&cli.server).cloned())?;
            let Some(id) = client.room_streams(&room).await?.into_iter().next() else {
                bail!("nobody is live in {room}");
            };
            let url = client.whep_url(&id).to_string();
            let token = client.token().map(str::to_owned);
            let report = tokio::task::spawn_blocking(move || {
                if let Some(dir) = &record {
                    std::fs::create_dir_all(dir)?;
                }
                cupstream_media::watch::watch(
                    &url,
                    token.as_deref(),
                    Duration::from_secs(seconds),
                    record.as_deref(),
                )
            })
            .await??;
            println!(
                "stream {id}: {} video frames ({}x{}), first after {:?}, longest gap {:?}; {} audio buffers",
                report.video_frames,
                report.width,
                report.height,
                report.first_frame.unwrap_or_default(),
                report.longest_gap,
                report.audio_buffers
            );
            Ok(())
        }
    }
}

// --- Remembered state --------------------------------------------------

fn config_dir() -> PathBuf {
    #[cfg(windows)]
    let base = std::env::var_os("APPDATA").map(PathBuf::from);
    #[cfg(not(windows))]
    let base = std::env::var_os("XDG_CONFIG_HOME")
        .map(PathBuf::from)
        .or_else(|| std::env::var_os("HOME").map(|h| PathBuf::from(h).join(".config")));
    base.unwrap_or_else(|| PathBuf::from("."))
        .join("cupstream-cli")
}

/// Small key-value files in the config directory: app session tokens by
/// server, and the portal's restore token. The app proper keeps its token
/// in the system keychain instead.
fn load(name: &str) -> BTreeMap<String, String> {
    std::fs::read_to_string(config_dir().join(name))
        .ok()
        .and_then(|s| serde_json::from_str(&s).ok())
        .unwrap_or_default()
}

fn save(name: &str, map: &BTreeMap<String, String>) -> anyhow::Result<()> {
    let dir = config_dir();
    std::fs::create_dir_all(&dir)?;
    let path = dir.join(name);
    std::fs::write(&path, serde_json::to_string_pretty(map)?)?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        std::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o600))?;
    }
    Ok(())
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
        .unwrap_or_else(|| "computer".into());
    format!("cupstream-cli on {host}")
}

fn open_browser(url: &str) {
    #[cfg(windows)]
    let r = std::process::Command::new("rundll32")
        .args(["url.dll,FileProtocolHandler", url])
        .spawn();
    #[cfg(not(windows))]
    let r = std::process::Command::new("xdg-open").arg(url).spawn();
    if let Err(e) = r {
        eprintln!("could not open the browser ({e}); open the address above yourself");
    }
}

// --- Commands ----------------------------------------------------------

async fn sign_in(server: &str) -> anyhow::Result<()> {
    let flow = SignIn::start(server, &device_name()).await?;
    eprintln!("Approve the sign-in in your browser:\n  {}", flow.url);
    open_browser(flow.url.as_str());
    let signed_in = flow.finish().await?;
    let mut tokens = load("tokens.json");
    tokens.insert(server.to_owned(), signed_in.token);
    save("tokens.json", &tokens)?;
    eprintln!(
        "Signed in as {} (@{}).",
        signed_in.user.display_name, signed_in.user.handle
    );
    Ok(())
}

async fn sign_out(server: &str) -> anyhow::Result<()> {
    let mut tokens = load("tokens.json");
    let Some(token) = tokens.remove(server) else {
        bail!("not signed in to {server}");
    };
    Client::new(server, Some(token))?.sign_out().await?;
    save("tokens.json", &tokens)?;
    eprintln!("Signed out.");
    Ok(())
}

fn parse_audio(s: &str) -> anyhow::Result<AudioMode> {
    Ok(match s {
        "except-discord" => AudioMode::ExceptDiscord,
        "all" => AudioMode::Everything,
        "test" => AudioMode::Test,
        "none" => AudioMode::None,
        _ => match s.strip_prefix("app:") {
            Some(app) if !app.is_empty() => AudioMode::App(app.to_owned()),
            _ => bail!("--audio is except-discord, all, app:<name>, test or none"),
        },
    })
}

fn parse_source(s: Option<&str>, restore_token: Option<String>) -> anyhow::Result<VideoSource> {
    let Some(s) = s else {
        return Ok(match capture::default_source() {
            #[cfg(target_os = "linux")]
            VideoSource::Portal { .. } => VideoSource::Portal { restore_token },
            other => other,
        });
    };
    let _ = &restore_token;
    let (kind, arg) = s.split_once(':').map_or((s, None), |(k, a)| (k, Some(a)));
    let num = |a: Option<&str>| -> anyhow::Result<u64> {
        let a = a.ok_or_else(|| anyhow!("{kind} needs a number"))?;
        Ok(match a.strip_prefix("0x") {
            Some(hex) => u64::from_str_radix(hex, 16)?,
            None => a.parse()?,
        })
    };
    Ok(match kind {
        "test" => VideoSource::Test,
        #[cfg(target_os = "linux")]
        "portal" => VideoSource::Portal { restore_token },
        #[cfg(target_os = "linux")]
        "x11" => VideoSource::X11 {
            window: arg.map(|a| num(Some(a))).transpose()?,
        },
        #[cfg(windows)]
        "monitor" => {
            VideoSource::Monitor(arg.map(|a| num(Some(a))).transpose()?.unwrap_or(0) as u32)
        }
        #[cfg(windows)]
        "window" => VideoSource::Window(num(arg)?),
        _ => bail!("unknown source {s}"),
    })
}

async fn publish(server: &str, args: PublishArgs) -> anyhow::Result<()> {
    cupstream_media::init()?;

    // Pick the encoder before asking for the screen, so a missing encoder
    // fails early.
    let codec = match args.codec {
        CodecArg::H264 => Codec::H264,
        CodecArg::Av1 => Codec::Av1,
    };
    let (enc, skipped) = encoder::choose(codec, args.encoder.as_deref())?;
    for s in &skipped {
        eprintln!("skipped encoder {s}");
    }
    eprintln!("encoder: {} ({})", enc.label, enc.factory);

    let mut portal = load("portal.json");
    let source = parse_source(
        args.source.as_deref(),
        if args.pick {
            None
        } else {
            portal.get("restore").cloned()
        },
    )?;
    let cap = capture::open(&source, args.fps).await?;
    if let Some(t) = &cap.restore_token {
        portal.insert("restore".into(), t.clone());
        save("portal.json", &portal)?;
    }
    let audio = audio::open(&parse_audio(&args.audio)?)?;
    for w in audio.iter().flat_map(|a| &a.warnings) {
        eprintln!("warning: {w}");
    }

    // Only now create the stream, so its token is not wasted on a failed
    // capture.
    let client = Client::new(server, load("tokens.json").get(server).cloned())?;
    let dest: Destination = match &args.stream_key {
        Some(key) => client.stream_key_destination(key, args.room.as_deref()),
        None => client
            .start_stream(&StartOptions {
                room: args.room.clone(),
                link_fallback: true,
                replace: args.replace,
            })
            .await
            .context(
                "starting the stream (sign in with `cupstream-cli sign-in`, or pass --stream-key)",
            )?,
    };

    let publisher = Publisher::new(
        cap,
        audio,
        enc,
        VideoSettings {
            bitrate_kbps: args.bitrate,
            fps: args.fps,
            height: args.height,
            keyframe_secs: args.keyframe_interval,
        },
        &Endpoint {
            url: dest.whip_url.clone(),
            token: dest.token.clone(),
        },
    )?;
    publisher.start()?;
    match &dest.room_slug {
        Some(slug) => eprintln!("live: {}", client.room_url(slug)),
        None => eprintln!("publishing to {}", dest.whip_url),
    }

    let result = run_until_stopped(&publisher, args.duration.map(Duration::from_secs)).await;
    // Stopping sends the WHIP DELETE, which ends the stream; ending it
    // through the API as well covers a pipeline that died without one.
    publisher.stop();
    if let Some(id) = &dest.stream_id
        && let Err(e) = client.end_stream(id).await
        && e.downcast_ref::<cupstream_client::ApiError>()
            .is_none_or(|e| e.status != 404)
    {
        eprintln!("ending the stream failed: {e:#}");
    }
    result
}

/// Follows the pipeline until Ctrl+C, the duration, or an error; prints
/// changes in captured audio.
async fn run_until_stopped(p: &Publisher, duration: Option<Duration>) -> anyhow::Result<()> {
    let bus = p.bus();
    let deadline = duration.map(|d| tokio::time::Instant::now() + d);
    let mut tick = tokio::time::interval(Duration::from_millis(100));
    let mut last_audio = String::new();
    let ctrl_c = tokio::signal::ctrl_c();
    tokio::pin!(ctrl_c);
    loop {
        tokio::select! {
            _ = &mut ctrl_c => return Ok(()),
            _ = tick.tick() => {}
        }
        if deadline.is_some_and(|d| tokio::time::Instant::now() >= d) {
            return Ok(());
        }
        while let Some(msg) = bus.pop() {
            match msg.view() {
                gst::MessageView::Error(e) => {
                    p.dump_dot("cupstream-error");
                    bail!(
                        "{} ({})",
                        e.error(),
                        e.debug().map(|d| d.to_string()).unwrap_or_default()
                    );
                }
                gst::MessageView::Warning(w) => eprintln!("warning: {}", w.error()),
                gst::MessageView::Eos(_) => return Ok(()),
                _ => {}
            }
        }
        if let Some(a) = p.audio() {
            let now = describe_audio(&a.streams());
            if now != last_audio {
                eprintln!("audio: {now}");
                last_audio = now;
            }
        }
    }
}

fn describe_audio(streams: &[audio::AudioStream]) -> String {
    if streams.is_empty() {
        return "no application is playing".into();
    }
    streams
        .iter()
        .map(|s| format!("{}{}", s.name, if s.captured { "" } else { " (left out)" }))
        .collect::<Vec<_>>()
        .join(", ")
}

async fn show_audio_until_ctrl_c(capture: &audio::AudioCapture) {
    // WASAPI leaves one process tree out without listing what plays; the
    // log above says which process that is.
    if cfg!(windows) {
        println!("capturing; Ctrl+C to stop");
        let _ = tokio::signal::ctrl_c().await;
        return;
    }
    let mut last = String::new();
    let ctrl_c = tokio::signal::ctrl_c();
    tokio::pin!(ctrl_c);
    loop {
        let now = describe_audio(&capture.streams());
        if now != last {
            println!("{now}");
            last = now;
        }
        tokio::select! {
            _ = &mut ctrl_c => return,
            _ = tokio::time::sleep(Duration::from_millis(250)) => {}
        }
    }
}
