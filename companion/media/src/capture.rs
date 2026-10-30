//! Screen capture sources. On Wayland the system's screen-sharing dialog
//! (xdg-desktop-portal) picks the screen or window and PipeWire delivers
//! it; a restore token avoids asking again. X11 uses ximagesrc, Windows
//! Windows.Graphics.Capture through d3d11screencapturesrc.

use anyhow::Context;

/// What to capture.
#[derive(Clone, Debug)]
pub enum VideoSource {
    /// A moving test pattern (CI and pipeline tests).
    Test,
    /// The screen-sharing portal (Wayland, and X11 desktops that have it).
    #[cfg(target_os = "linux")]
    Portal {
        /// From an earlier capture, to skip the dialog.
        restore_token: Option<String>,
    },
    /// The whole X11 screen, or one window by its XID.
    #[cfg(target_os = "linux")]
    X11 { window: Option<u64> },
    /// A monitor by index.
    #[cfg(windows)]
    Monitor(u32),
    /// A window by its HWND.
    #[cfg(windows)]
    Window(u64),
}

/// An open capture: a source bin with one raw video pad, and whatever must
/// stay alive while it runs (the portal session).
pub struct Capture {
    pub bin: gst::Bin,
    /// A token to capture the same source again without the dialog.
    pub restore_token: Option<String>,
    _keep: Box<dyn std::any::Any + Send>,
}

/// The X11 or Wayland session, from the environment.
#[cfg(target_os = "linux")]
pub fn is_wayland() -> bool {
    std::env::var_os("WAYLAND_DISPLAY").is_some()
        || std::env::var("XDG_SESSION_TYPE").is_ok_and(|t| t == "wayland")
}

/// The default source for this system.
pub fn default_source() -> VideoSource {
    #[cfg(target_os = "linux")]
    return if is_wayland() {
        VideoSource::Portal {
            restore_token: None,
        }
    } else {
        VideoSource::X11 { window: None }
    };
    #[cfg(windows)]
    return VideoSource::Monitor(0);
    #[allow(unreachable_code)]
    VideoSource::Test
}

fn bin_from(desc: &str) -> anyhow::Result<gst::Bin> {
    gst::parse::bin_from_description(desc, true).with_context(|| format!("creating {desc}"))
}

/// Opens a source. For the portal this shows the system dialog, unless the
/// restore token is still valid.
pub async fn open(source: &VideoSource, fps: u32) -> anyhow::Result<Capture> {
    // Screen sources only deliver frames when something changes; dropping
    // frames above the frame rate keeps the encoder's rate control sane.
    let rate = format!("videorate drop-only=true max-rate={fps}");
    match source {
        VideoSource::Test => Ok(Capture {
            bin: bin_from(&format!(
                "videotestsrc is-live=true pattern=ball ! capsfilter caps=video/x-raw,width=1920,height=1080,framerate={fps}/1"
            ))?,
            restore_token: None,
            _keep: Box::new(()),
        }),
        #[cfg(target_os = "linux")]
        VideoSource::Portal { restore_token } => {
            portal::open(restore_token.as_deref(), &rate).await
        }
        #[cfg(target_os = "linux")]
        VideoSource::X11 { window } => {
            let xid = window.map(|w| format!(" xid={w}")).unwrap_or_default();
            Ok(Capture {
                bin: bin_from(&format!(
                    "ximagesrc use-damage=false show-pointer=true{xid} ! {rate}"
                ))?,
                restore_token: None,
                _keep: Box::new(()),
            })
        }
        #[cfg(windows)]
        VideoSource::Monitor(i) => Ok(Capture {
            bin: bin_from(&format!(
                "d3d11screencapturesrc capture-api=wgc show-cursor=true monitor-index={i} ! {rate}"
            ))?,
            restore_token: None,
            _keep: Box::new(()),
        }),
        #[cfg(windows)]
        VideoSource::Window(hwnd) => Ok(Capture {
            bin: bin_from(&format!(
                "d3d11screencapturesrc capture-api=wgc show-cursor=true window-handle={hwnd} ! {rate}"
            ))?,
            restore_token: None,
            _keep: Box::new(()),
        }),
    }
}

#[cfg(target_os = "linux")]
mod portal {
    use super::{Capture, bin_from};
    use anyhow::{Context, bail};
    use ashpd::desktop::PersistMode;
    use ashpd::desktop::screencast::{CursorMode, Screencast, SelectSourcesOptions, SourceType};
    use std::os::fd::{AsRawFd, OwnedFd};

    /// Keeps the portal session and the PipeWire connection open.
    struct Session {
        _fd: OwnedFd,
        _session: ashpd::desktop::Session<Screencast>,
    }

    pub async fn open(restore_token: Option<&str>, rate: &str) -> anyhow::Result<Capture> {
        let proxy = Screencast::new()
            .await
            .context("the screen-sharing portal is not available")?;
        let session = proxy.create_session(Default::default()).await?;
        proxy
            .select_sources(
                &session,
                SelectSourcesOptions::default()
                    .set_cursor_mode(CursorMode::Embedded)
                    .set_sources(SourceType::Monitor | SourceType::Window)
                    .set_multiple(false)
                    .set_restore_token(restore_token)
                    .set_persist_mode(PersistMode::ExplicitlyRevoked),
            )
            .await?
            .response()
            .context("choosing what to share")?;
        let streams = proxy
            .start(&session, None, Default::default())
            .await?
            .response()
            .context("sharing was cancelled")?;
        let Some(stream) = streams.streams().first() else {
            bail!("nothing was chosen to share");
        };
        let node = stream.pipe_wire_node_id();
        let restore_token = streams.restore_token().map(str::to_owned);
        let fd = proxy
            .open_pipe_wire_remote(&session, Default::default())
            .await?;

        // keepalive-time repeats the last frame when the screen does not
        // change, so a viewer asking for a keyframe never waits long.
        let bin = bin_from(&format!(
            "pipewiresrc fd={} path={node} do-timestamp=true keepalive-time=500 ! {rate}",
            fd.as_raw_fd()
        ))?;
        Ok(Capture {
            bin,
            restore_token,
            _keep: Box::new(Session {
                _fd: fd,
                _session: session,
            }),
        })
    }
}

/// Whether a source element exists, for messages about missing plugins.
pub fn element_available(name: &str) -> bool {
    gst::ElementFactory::find(name).is_some()
}
