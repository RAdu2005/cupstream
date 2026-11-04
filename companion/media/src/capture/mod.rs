//! Screen capture sources. On Wayland the system's screen-sharing dialog
//! (xdg-desktop-portal) picks the screen or window and PipeWire delivers
//! it; a restore token avoids asking again. X11 uses ximagesrc, Windows
//! Windows.Graphics.Capture through d3d11screencapturesrc.

use anyhow::Context;
use gst::prelude::*;

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
    /// A PipeWire video node by name or serial, without the portal: for
    /// testing the capture path with any PipeWire video source.
    #[cfg(target_os = "linux")]
    PipeWire { target: String },
    /// A monitor by index.
    #[cfg(windows)]
    Monitor(u32),
    /// A monitor by its HMONITOR (what the app's picker lists).
    #[cfg(windows)]
    MonitorHandle(u64),
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

/// What every screen source ends with: square pixels. pipewiresrc leaves
/// the pixel aspect ratio out of its caps, and negotiation then fills it in
/// from the converters' accepted range, fixating to its lower bound,
/// 1/2147483647: videoconvertscale then fixates a 1-pixel width and
/// vapostproc fails to compute the display aspect ratio ("integer
/// overflow"), on every start. Screens have square pixels, so say so;
/// (ANY) keeps DMA-BUF and D3D11 memory.
pub(crate) fn capture_tail() -> &'static str {
    "capsfilter caps=\"video/x-raw(ANY),pixel-aspect-ratio=1/1\""
}

/// Drops frames that come sooner after the last one kept than `fps`
/// allows, by their timestamps. Screen sources deliver frames when
/// something changes, sometimes faster than the stream's frame rate. This
/// replaces videorate, which aborts the process in GStreamer 1.24 on the
/// variable frame rate (0/1) screen captures report.
pub(crate) fn limit_frame_rate(pad: &gst::Pad, fps: u32) {
    // A quarter of a frame of slack, so a source at exactly the frame rate
    // keeps every frame despite jitter.
    let min_gap = 1_000_000_000 / u64::from(fps.max(1)) * 3 / 4;
    let last = std::sync::Mutex::new(None::<u64>);
    pad.add_probe(gst::PadProbeType::BUFFER, move |_, info| {
        let Some(pts) = info.buffer().and_then(|b| b.pts()).map(|t| t.nseconds()) else {
            return gst::PadProbeReturn::Ok;
        };
        let mut last = last.lock().unwrap();
        if let Some(l) = *last
            && pts >= l
            && pts - l < min_gap
        {
            return gst::PadProbeReturn::Drop;
        }
        *last = Some(pts);
        gst::PadProbeReturn::Ok
    });
}

/// Opens a source. For the portal this shows the system dialog, unless the
/// restore token is still valid.
///
/// `copy`: copy each frame out of PipeWire's buffer at once (pipewiresrc
/// `always-copy`), for encoders that take frames in system memory. PipeWire
/// then shares its frames in shared memory, and the compositor's pool is
/// small (KWin: 4 buffers): holding them while frames wait in queues and get
/// converted makes the compositor skip frames (30 instead of 60 fps).
/// Encoders on the GPU get DMA-BUF frames and copy them on the GPU, right
/// after the capture (see `Candidate::capture_copy_description`).
pub async fn open(source: &VideoSource, fps: u32, copy: bool) -> anyhow::Result<Capture> {
    let capture = open_source(source, fps, copy).await?;
    if let Some(pad) = capture.bin.static_pad("src") {
        limit_frame_rate(&pad, fps);
    }
    Ok(capture)
}

async fn open_source(source: &VideoSource, fps: u32, copy: bool) -> anyhow::Result<Capture> {
    let rate = capture_tail();
    // A pipewiresrc option: Linux only.
    #[cfg(target_os = "linux")]
    let copy = if copy { " always-copy=true" } else { "" };
    #[cfg(not(target_os = "linux"))]
    let _ = copy;
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
            portal::open(restore_token.as_deref(), rate, copy).await
        }
        #[cfg(target_os = "linux")]
        VideoSource::PipeWire { target } => Ok(Capture {
            bin: bin_from(&format!(
                "pipewiresrc target-object={target} do-timestamp=true keepalive-time=500{copy} ! {rate}"
            ))?,
            restore_token: None,
            _keep: Box::new(()),
        }),
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
        VideoSource::MonitorHandle(h) => Ok(Capture {
            bin: bin_from(&format!(
                "d3d11screencapturesrc capture-api=wgc show-cursor=true monitor-handle={h} ! {rate}"
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

#[cfg(windows)]
pub mod windows;

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

    pub async fn open(
        restore_token: Option<&str>,
        rate: &str,
        copy: &str,
    ) -> anyhow::Result<Capture> {
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
            "pipewiresrc fd={} path={node} do-timestamp=true keepalive-time=500{copy} ! {rate}",
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

#[cfg(test)]
mod tests {
    use super::*;

    /// A source negotiating like pipewiresrc, whose formats leave out the
    /// pixel aspect ratio: it intersects them with what downstream accepts
    /// and fixates the result. Then through the capture tail (or not) and
    /// the converter in front of a software encoder, scaled to 720p.
    /// Returns the size that comes out, or the error.
    fn convert(
        cand: &'static crate::encoder::Candidate,
        height: Option<u32>,
        with_tail: bool,
    ) -> anyhow::Result<(u32, u32)> {
        crate::init()?;
        let upload = cand.upload_description(height);
        let tail = if with_tail {
            format!("{} ! ", capture_tail())
        } else {
            String::new()
        };
        let desc =
            format!("appsrc name=src is-live=true format=time ! {tail}{upload} ! appsink name=out");
        let pipeline = gst::parse::launch(&desc)?
            .downcast::<gst::Pipeline>()
            .unwrap();
        let src = pipeline
            .by_name("src")
            .unwrap()
            .downcast::<gst_app::AppSrc>()
            .unwrap();
        let out = pipeline
            .by_name("out")
            .unwrap()
            .downcast::<gst_app::AppSink>()
            .unwrap();

        // gst_pipewire_src_negotiate: possible formats, intersected with
        // the peer's caps, fixated. KWin's, as logged on a 2560x1600 screen.
        let formats: gst::Caps =
            "video/x-raw,format=BGRA,width=2560,height=1600,framerate=0/1,max-framerate=60/1"
                .parse()?;
        let peer = src
            .static_pad("src")
            .unwrap()
            .peer_query_caps(Some(&formats));
        let mut caps = formats.intersect(&peer);
        caps.fixate();
        src.set_caps(Some(&caps));

        pipeline.set_state(gst::State::Playing)?;
        for i in 0..3u64 {
            let mut buf = gst::Buffer::with_size(2560 * 1600 * 4)?;
            buf.get_mut()
                .unwrap()
                .set_pts(gst::ClockTime::from_mseconds(i * 16));
            let _ = src.push_buffer(buf);
        }
        let sample = out.try_pull_sample(gst::ClockTime::from_seconds(2));
        let error = pipeline
            .bus()
            .unwrap()
            .pop_filtered(&[gst::MessageType::Error]);
        let _ = pipeline.set_state(gst::State::Null);
        if let Some(msg) = error
            && let gst::MessageView::Error(e) = msg.view()
        {
            anyhow::bail!(
                "{} (caps {caps})",
                e.debug()
                    .map(|d| d.to_string())
                    .unwrap_or_else(|| e.error().to_string())
            );
        }
        let caps = sample
            .ok_or_else(|| anyhow::anyhow!("no frame"))?
            .caps()
            .unwrap()
            .to_owned();
        let info = gst_video::VideoInfo::from_caps(&caps)?;
        Ok((info.width(), info.height()))
    }

    #[test]
    fn open_pixel_aspect_ratio_is_square() {
        crate::init().unwrap();
        let x264 = crate::encoder::Candidate::by_factory("x264enc").unwrap();
        // Without the tail, the converter fails the way the app did.
        let err = convert(x264, Some(1080), false).unwrap_err().to_string();
        assert!(err.contains("integer overflow"), "{err}");
        // With it, each converter this machine has scales 2560x1600 by its
        // height (x264 and OpenH264 share the software one).
        let mut tried = 0;
        for name in ["x264enc", "vah264enc", "nvh264enc"] {
            let Some(cand) =
                crate::encoder::Candidate::by_factory(name).filter(|c| c.probe().is_ok())
            else {
                continue;
            };
            tried += 1;
            for (height, want) in [
                (Some(1080), (1728, 1080)),
                (Some(720), (1152, 720)),
                (None, (2560, 1600)),
            ] {
                let got = convert(cand, height, true)
                    .unwrap_or_else(|e| panic!("{name} at {height:?}: {e:#}"));
                assert_eq!(got, want, "{name} at {height:?}");
            }
        }
        assert!(tried > 0);
    }

    #[test]
    fn frame_rate_is_limited_by_timestamps() {
        crate::init().unwrap();
        let pipeline = gst::parse::launch(
            "appsrc name=src format=time caps=video/x-raw,format=BGRx,width=64,height=64,framerate=0/1 \
             ! fakesink name=out sync=false signal-handoffs=true",
        )
        .unwrap()
        .downcast::<gst::Pipeline>()
        .unwrap();
        let src = pipeline
            .by_name("src")
            .unwrap()
            .downcast::<gst_app::AppSrc>()
            .unwrap();
        limit_frame_rate(&src.static_pad("src").unwrap(), 60);
        let kept = std::sync::Arc::new(std::sync::atomic::AtomicU32::new(0));
        let k = kept.clone();
        pipeline
            .by_name("out")
            .unwrap()
            .connect("handoff", false, move |_| {
                k.fetch_add(1, std::sync::atomic::Ordering::Relaxed);
                None
            });
        pipeline.set_state(gst::State::Playing).unwrap();
        // 200 frames 5 ms apart: a 200 Hz source for one second.
        for i in 0..200u64 {
            let mut buf = gst::Buffer::with_size(64 * 64 * 4).unwrap();
            buf.get_mut()
                .unwrap()
                .set_pts(gst::ClockTime::from_mseconds(i * 5));
            src.push_buffer(buf).unwrap();
        }
        src.end_of_stream().unwrap();
        let _ = pipeline
            .bus()
            .unwrap()
            .timed_pop_filtered(gst::ClockTime::from_seconds(5), &[gst::MessageType::Eos]);
        let _ = pipeline.set_state(gst::State::Null);
        // Every third frame (15 ms apart) passes the 12.5 ms minimum gap.
        let kept = kept.load(std::sync::atomic::Ordering::Relaxed);
        assert!((60..=70).contains(&kept), "kept {kept} of 200");
    }
}
