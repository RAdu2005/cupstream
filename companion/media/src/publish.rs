//! The publishing pipeline: capture, conversion, encoding and WHIP.
//!
//! Encoding happens here rather than inside whipclientsink, so the app picks
//! the encoder (with fallbacks), configures it for live streaming and
//! changes its bitrate while live. whipclientsink only packetizes and sends;
//! viewers' keyframe requests (PLI) reach the encoder as force-key-unit
//! events through it.

use anyhow::{Context, anyhow};
use gst::glib;
use gst::prelude::*;

use crate::audio::AudioCapture;
use crate::capture::Capture;
use crate::encoder::{self, Candidate, Codec};
use crate::frames::FrameClock;
use crate::low::{LowCtl, LowSettings, LowState};

/// Where to publish: a WHIP endpoint and its Bearer token.
#[derive(Clone, Debug)]
pub struct Endpoint {
    pub url: String,
    pub token: String,
}

/// Video settings.
#[derive(Clone, Copy, Debug)]
pub struct VideoSettings {
    pub bitrate_kbps: u32,
    pub fps: u32,
    /// Scale to this height, keeping the aspect ratio; None keeps the
    /// source's size.
    pub height: Option<u32>,
    pub keyframe_secs: u32,
}

/// Opus bitrate: transparent for game and music audio in stereo.
const AUDIO_BITRATE: i32 = 128_000;

/// A running publisher.
pub struct Publisher {
    pipeline: gst::Pipeline,
    encoder: gst::Element,
    pub encoder_info: &'static Candidate,
    capture: Capture,
    /// The converter in front of the encoder; its capsfilter "size" sets
    /// the encoded size.
    upload: gst::Bin,
    video_tee: gst::Element,
    /// What the stream is encoded at now (see `set_video`).
    height: Option<u32>,
    fps: u32,
    audio: Option<AudioCapture>,
    low: Option<LowCtl>,
    /// The capture's own pipeline and the frame clock (not on Windows yet:
    /// D3D11 frames would need the device shared between the pipelines).
    frames: Option<FrameClock>,
}

impl Publisher {
    /// Builds the pipeline; `start` sets it playing.
    pub fn new(
        capture: Capture,
        audio: Option<AudioCapture>,
        encoder_info: &'static Candidate,
        video: VideoSettings,
        endpoint: &Endpoint,
        low: Option<LowSettings>,
    ) -> anyhow::Result<Self> {
        let pipeline = gst::Pipeline::with_name("cupstream");

        let sink = gst::ElementFactory::make("whipclientsink")
            .name("whip")
            .build()
            .context("whipclientsink is missing; call cupstream_media::init() first")?;
        let signaller = sink.property::<glib::Object>("signaller");
        signaller.set_property("whip-endpoint", &endpoint.url);
        signaller.set_property("auth-token", &endpoint.token);
        // The encoder is ours; MediaMTX answers with its own candidates, so
        // no STUN server is contacted.
        sink.set_property_from_str("congestion-control", "disabled");
        sink.set_property("stun-server", None::<String>);
        pipeline.add(&sink)?;

        // Video: capture, a queue that drops frames rather than adding
        // latency when the encoder falls behind, upload and conversion,
        // encoder, parser.
        let queue = gst::ElementFactory::make("queue")
            .property_from_str("leaky", "downstream")
            .property("max-size-buffers", 2u32)
            .property("max-size-time", 0u64)
            .property("max-size-bytes", 0u32)
            .build()?;
        let upload =
            gst::parse::bin_from_description(&encoder_info.upload_description(video.height), true)?;
        let enc = encoder_info.make(&encoder::Settings {
            bitrate_kbps: video.bitrate_kbps,
            fps: video.fps,
            keyframe_secs: video.keyframe_secs,
        })?;
        let caps = gst::ElementFactory::make("capsfilter")
            .property("caps", encoder_info.output_caps())
            .build()?;
        let parse = match encoder_info.codec {
            Codec::H264 => gst::ElementFactory::make("h264parse")
                .property("config-interval", -1i32)
                .build()?,
            Codec::Av1 => gst::ElementFactory::make("av1parse").build()?,
        };
        // The capture feeds the stream and, later, its low-quality version.
        let video_tee = gst::ElementFactory::make("tee")
            .name("video-tee")
            .property("allow-not-linked", true)
            .build()?;
        let copy = encoder_info
            .copy_description()
            .map(|d| gst::parse::bin_from_description(d, true))
            .transpose()?;
        let frames = if cfg!(windows) {
            None
        } else {
            Some(FrameClock::new(
                &capture.bin,
                encoder_info.capture_copy_description(),
                encoder_info.capture_caps(),
                video.fps,
                pipeline.bus().expect("pipelines have a bus"),
            )?)
        };
        // Where the publishing pipeline's video comes from: the frame clock,
        // or the capture itself.
        let video_in: gst::Element = match &frames {
            Some(f) => f.source.clone().upcast(),
            None => capture.bin.clone().upcast(),
        };
        let capture_out: gst::Element = match &copy {
            Some(c) => c.clone().upcast(),
            None => video_in.clone(),
        };
        pipeline.add_many([
            &video_in,
            &video_tee,
            &queue,
            upload.upcast_ref(),
            &enc,
            &caps,
            &parse,
        ])?;
        if let Some(copy) = &copy {
            pipeline.add(copy)?;
            video_in.link(copy)?;
        }
        gst::Element::link_many([
            &capture_out,
            &video_tee,
            &queue,
            upload.upcast_ref(),
            &enc,
            &caps,
            &parse,
            &sink,
        ])?;
        log_keyframe_requests(&enc);
        keep_caps_stable(&parse);
        if let (Some(size), Some(input)) = (upload.by_name("size"), upload.static_pad("sink")) {
            lock_size(&input, size, video.height);
        }

        if let Some(audio) = &audio {
            let queue = gst::ElementFactory::make("queue").build()?;
            let opus = gst::ElementFactory::make("opusenc")
                .property("bitrate", AUDIO_BITRATE)
                .property("inband-fec", true)
                .property_from_str("frame-size", "10")
                .build()?;
            // Encoded once; the low-quality version sends the same Opus.
            let audio_tee = gst::ElementFactory::make("tee")
                .name("audio-tee")
                .property("allow-not-linked", true)
                .build()?;
            let out = gst::ElementFactory::make("queue").build()?;
            pipeline.add_many([audio.bin.upcast_ref(), &queue, &opus, &audio_tee, &out])?;
            gst::Element::link_many([
                audio.bin.upcast_ref(),
                &queue,
                &opus,
                &audio_tee,
                &out,
                &sink,
            ])?;
        }

        let low = low.map(|settings| {
            let ctl = LowCtl::new(
                &pipeline,
                video_tee.clone(),
                pipeline.by_name("audio-tee"),
                encoder_info,
                settings,
            );
            ctl.start_when_connected(&sink);
            ctl
        });

        Ok(Self {
            pipeline,
            encoder: enc,
            encoder_info,
            capture,
            upload,
            video_tee,
            height: video.height,
            fps: video.fps,
            audio,
            low,
            frames,
        })
    }

    pub fn start(&self) -> anyhow::Result<()> {
        // Hand frames to the network as soon as they are encoded. Left to
        // itself the pipeline configures its sinks with the latency its
        // elements report (about 50 ms here), and whipclientsink's input
        // (a clocksync and an appsink) holds every frame that long. That
        // only delays the stream, and with the leaky queue before the
        // encoder holding two frames, it also drops every frame beyond
        // two per hold: about 30 fps of a 60 fps capture.
        self.pipeline.set_latency(Some(gst::ClockTime::ZERO));
        // Off the async runtime, like stop: a sink that fails to start
        // cleans up inside this call.
        let pipeline = &self.pipeline;
        let started =
            std::thread::scope(|s| s.spawn(|| pipeline.set_state(gst::State::Playing)).join());
        if !matches!(started, Ok(Ok(_))) {
            // The element that refused posted why.
            let why = self
                .bus()
                .iter_filtered(&[gst::MessageType::Error])
                .find_map(|msg| match msg.view() {
                    gst::MessageView::Error(e) => Some(format!(
                        "{}: {}",
                        msg.src().map(|s| s.name().to_string()).unwrap_or_default(),
                        e.error()
                    )),
                    _ => None,
                });
            self.stop();
            return Err(anyhow!(
                "the pipeline did not start ({})",
                why.as_deref().unwrap_or("no reason given")
            ));
        }
        if let Some(frames) = &self.frames
            && let Err(e) = frames.start(&self.pipeline)
        {
            self.stop();
            return Err(e);
        }
        if let Some(sink) = self.pipeline.by_name("whip") {
            keyframes_until_connected(&sink, &self.encoder);
        }
        Ok(())
    }

    /// Sorts out an error from the bus: errors of the low-quality version
    /// are handled here (it is removed and tried again) and give None; any
    /// other error stops the stream and is returned.
    pub fn check(&self, msg: &gst::Message) -> Option<String> {
        let gst::MessageView::Error(e) = msg.view() else {
            return None;
        };
        if self.low.as_ref().is_some_and(|l| l.handle_error(msg)) {
            return None;
        }
        // Name the element: the GStreamer message alone rarely says which.
        Some(format!(
            "{}: {} ({})",
            msg.src().map(|s| s.name().to_string()).unwrap_or_default(),
            e.error(),
            e.debug().map(|d| d.to_string()).unwrap_or_default()
        ))
    }

    /// Where the low-quality version stands; None when none is published.
    pub fn low_state(&self) -> Option<LowState> {
        self.low.as_ref().map(LowCtl::state)
    }
    pub fn bus(&self) -> gst::Bus {
        self.pipeline.bus().expect("pipelines have a bus")
    }

    /// Changes the video bitrate while live.
    pub fn set_bitrate(&self, kbps: u32) {
        encoder::set_bitrate(&self.encoder, kbps);
    }

    /// Shares another source while live, in the same session: viewers see
    /// the new source without reconnecting. The encoded size is fitted to
    /// the new source's first frame (and the stream's height), like the
    /// first source's.
    pub fn switch_capture(&mut self, capture: Capture) -> anyhow::Result<()> {
        capture.set_fps(self.fps);
        let refit = {
            let size = self.upload.by_name("size");
            let height = self.height;
            let low = self.low.clone();
            move |caps: &gst::CapsRef| {
                if let Some(size) = &size {
                    fit_size(size, caps, height);
                }
                if let Some(low) = &low {
                    low.refit(caps);
                }
            }
        };
        match &self.frames {
            Some(frames) => frames.switch(&capture.bin, move |caps| refit(caps))?,
            None => self.replace_capture_bin(&capture.bin, refit)?,
        }
        // The old capture stopped; its portal session can end.
        self.capture = capture;
        Ok(())
    }

    /// Without the frame clock (Windows): the new capture bin takes the old
    /// one's place in the pipeline. The old one stops first, since a
    /// running source whose pad gets unlinked fails the stream; the encoder
    /// gets no frames for that moment. Should the new one not link, the old
    /// one is put back. A new one that fails to start posts its error, and
    /// the stream reconnects with the old source.
    fn replace_capture_bin(
        &self,
        bin: &gst::Bin,
        refit: impl Fn(&gst::CapsRef) + Send + Sync + 'static,
    ) -> anyhow::Result<()> {
        let old = self.capture.bin.clone();
        self.pipeline.add(bin)?;
        std::thread::scope(|s| {
            s.spawn(|| {
                let _ = old.set_state(gst::State::Null);
            });
        });
        old.unlink(&self.video_tee);
        if let Err(e) = bin.link(&self.video_tee) {
            let _ = self.pipeline.remove(bin);
            let _ = old.link(&self.video_tee);
            let _ = old.sync_state_with_parent();
            return Err(e).context("linking the new capture");
        }
        let _ = self.pipeline.remove(&old);
        if let Some(pad) = bin.static_pad("src") {
            pad.add_probe(gst::PadProbeType::BUFFER, move |pad, _| {
                if let Some(caps) = pad.current_caps() {
                    refit(&caps);
                }
                gst::PadProbeReturn::Remove
            });
        }
        bin.sync_state_with_parent()
            .context("starting the new capture")?;
        Ok(())
    }

    /// Changes the resolution (the height to scale to; None keeps the
    /// source's size) and the frame rate while live. The low-quality
    /// version keeps its own.
    pub fn set_video(&mut self, height: Option<u32>, fps: u32) {
        self.height = height;
        self.fps = fps;
        self.capture.set_fps(fps);
        if let Some(frames) = &self.frames {
            frames.set_fps(fps);
        }
        if let (Some(size), Some(caps)) = (
            self.upload.by_name("size"),
            self.upload
                .static_pad("sink")
                .and_then(|p| p.current_caps()),
        ) {
            fit_size(&size, &caps, height);
        }
    }

    pub fn audio(&self) -> Option<&AudioCapture> {
        self.audio.as_ref()
    }

    /// Stops publishing; whipclientsink ends the WHIP session (DELETE).
    ///
    /// The state change runs on its own thread: whipclientsink ends the
    /// session with its own async runtime and must not be stopped from
    /// inside another one (the app's and the CLI's tokio).
    pub fn stop(&self) {
        if let Some(low) = &self.low {
            low.stop();
        }
        if let Some(frames) = &self.frames {
            frames.stop();
        }
        let pipeline = &self.pipeline;
        std::thread::scope(|s| {
            s.spawn(|| {
                let _ = pipeline.set_state(gst::State::Null);
            });
        });
    }

    /// Writes a Graphviz file of the pipeline (GST_DEBUG_DUMP_DOT_DIR).
    pub fn dump_dot(&self, name: &str) {
        self.pipeline
            .debug_to_dot_file_with_ts(gst::DebugGraphDetails::all(), name);
    }
}

impl Drop for Publisher {
    fn drop(&mut self) {
        self.stop();
    }
}

/// Before the WHIP session exists, whipclientsink inspects the stream and
/// waits for a keyframe, which with a long keyframe interval takes seconds.
/// Ask for one every 250 ms until the session is up and MediaMTX asks
/// itself.
pub(crate) fn keyframes_until_connected(sink: &gst::Element, encoder: &gst::Element) {
    let connected = std::sync::Arc::new(std::sync::atomic::AtomicBool::new(false));
    let c = connected.clone();
    sink.connect("consumer-added", false, move |_| {
        c.store(true, std::sync::atomic::Ordering::Relaxed);
        None
    });
    let Some(pad) = encoder.static_pad("src") else {
        return;
    };
    let pad = pad.downgrade();
    std::thread::spawn(move || {
        for _ in 0..120 {
            if connected.load(std::sync::atomic::Ordering::Relaxed) {
                return;
            }
            let Some(pad) = pad.upgrade() else { return };
            pad.send_event(
                gst_video::UpstreamForceKeyUnitEvent::builder()
                    .all_headers(true)
                    .build(),
            );
            drop(pad);
            std::thread::sleep(std::time::Duration::from_millis(250));
        }
    });
}

/// whipclientsink cannot renegotiate: of the encoded caps, only the size
/// and frame rate may change while live. Some parsers (h264parse in
/// GStreamer 1.24) first pass the encoder's caps on and add these fields
/// once they parsed the SPS; they are dropped so both caps are the same.
/// A larger size or frame rate also raises the H.264 level, which
/// whipclientsink refuses, so the caps keep the first level.
pub(crate) fn keep_caps_stable(parse: &gst::Element) {
    const ADDED_LATER: [&str; 4] = [
        "coded-picture-structure",
        "chroma-format",
        "bit-depth-luma",
        "bit-depth-chroma",
    ];
    let Some(pad) = parse.static_pad("src") else {
        return;
    };
    let level = std::sync::Mutex::new(None::<String>);
    pad.add_probe(gst::PadProbeType::EVENT_DOWNSTREAM, move |_, info| {
        if let Some(gst::PadProbeData::Event(ev)) = &mut info.data
            && let gst::EventView::Caps(c) = ev.view()
        {
            let mut caps = c.caps().to_owned();
            let mut level = level.lock().unwrap();
            for s in caps.make_mut().iter_mut() {
                s.remove_fields(ADDED_LATER);
                match (&*level, s.get::<String>("level")) {
                    (Some(first), Ok(_)) => s.set("level", first.as_str()),
                    (None, Ok(l)) => *level = Some(l),
                    _ => {}
                }
            }
            *ev = gst::event::Caps::new(&caps);
        }
        gst::PadProbeReturn::Ok
    });
}

/// The size to encode a `width`x`height` source at: scaled to `height`
/// when given and smaller (never up), keeping the aspect ratio, and
/// rounded down to even numbers. 4:2:0 H.264 needs even dimensions, and
/// screens are not always even: 2560x1600 at a 1.5 scale is 1707x1067,
/// which x264 refuses and VA-API cannot fixate.
pub(crate) fn output_size(width: u32, height: u32, target_height: Option<u32>) -> (u32, u32) {
    let (w, h) = match target_height {
        Some(t) if t < height => {
            let w = (u64::from(width) * u64::from(t) + u64::from(height) / 2) / u64::from(height);
            (w as u32, t)
        }
        _ => (width, height),
    };
    ((w & !1).max(2), (h & !1).max(2))
}

/// Sets the capsfilter `size` after the converter to the size
/// [`output_size`] computes for a source with these caps. False when the
/// caps have no usable size.
pub(crate) fn fit_size(
    size: &gst::Element,
    source: &gst::CapsRef,
    target_height: Option<u32>,
) -> bool {
    let dims = source
        .structure(0)
        .and_then(|s| Some((s.get::<i32>("width").ok()?, s.get::<i32>("height").ok()?)));
    let Some((w, h)) = dims.filter(|(w, h)| *w > 0 && *h > 0) else {
        return false;
    };
    let (w, h) = output_size(w as u32, h as u32, target_height);
    let mut caps = size.property::<gst::Caps>("caps");
    if let Some(st) = caps.make_mut().structure_mut(0) {
        if st.get::<i32>("width") == Ok(w as i32) && st.get::<i32>("height") == Ok(h as i32) {
            return true;
        }
        st.set("width", w as i32);
        st.set("height", h as i32);
    }
    tracing::debug!("encoding {w}x{h}");
    size.set_property("caps", caps);
    true
}

/// Fixes the encoded size from the first frame's: the capsfilter `size`
/// after the converter gets the size [`output_size`] computes, before the
/// converter negotiates. Later, when a captured window is resized, its
/// frames are scaled (with borders) to the same size rather than changing
/// the encoder's size with every step of the resize. `input` is the
/// converter's sink.
pub(crate) fn lock_size(input: &gst::Pad, size: gst::Element, target_height: Option<u32>) {
    let size = size.downgrade();
    input.add_probe(gst::PadProbeType::EVENT_DOWNSTREAM, move |_, info| {
        let Some(gst::PadProbeData::Event(ev)) = &info.data else {
            return gst::PadProbeReturn::Ok;
        };
        let gst::EventView::Caps(c) = ev.view() else {
            return gst::PadProbeReturn::Ok;
        };
        let Some(size) = size.upgrade() else {
            return gst::PadProbeReturn::Remove;
        };
        // A frame without a usable size cannot fix it; wait for the next.
        if fit_size(&size, c.caps(), target_height) {
            gst::PadProbeReturn::Remove
        } else {
            gst::PadProbeReturn::Ok
        }
    });
}

/// Logs the keyframe requests that reach the encoder: a viewer joined or
/// lost packets and MediaMTX asked the sender for a keyframe.
fn log_keyframe_requests(enc: &gst::Element) {
    let Some(pad) = enc.static_pad("src") else {
        return;
    };
    pad.add_probe(gst::PadProbeType::EVENT_UPSTREAM, |_, info| {
        if let Some(gst::PadProbeData::Event(ev)) = &info.data
            && gst_video::UpstreamForceKeyUnitEvent::parse(ev).is_ok()
        {
            tracing::debug!("keyframe requested");
        }
        gst::PadProbeReturn::Ok
    });
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn output_sizes_are_even_and_never_upscaled() {
        // 2560x1600 at a 1.5 scale.
        assert_eq!(output_size(1707, 1067, None), (1706, 1066));
        assert_eq!(output_size(1707, 1067, Some(720)), (1152, 720));
        assert_eq!(output_size(1707, 1067, Some(1080)), (1706, 1066));
        assert_eq!(output_size(1707, 1067, Some(1067)), (1706, 1066));
        assert_eq!(output_size(2560, 1600, Some(1080)), (1728, 1080));
        assert_eq!(output_size(1920, 1080, Some(1080)), (1920, 1080));
        // An odd target height (cupstream-cli --height).
        assert_eq!(output_size(1920, 1080, Some(719)), (1278, 718));
        assert_eq!(output_size(1, 1, None), (2, 2));
    }

    /// An odd-sized (1707x1067) source through the capture tail, the
    /// converter and the encoder, as the publisher links them: the encoded
    /// size, or the error.
    fn encode(cand: &'static Candidate, height: Option<u32>) -> anyhow::Result<(i32, i32)> {
        crate::init()?;
        let pipeline = gst::Pipeline::new();
        let src = gst::parse::bin_from_description(
            &format!(
                "appsrc name=src is-live=true format=time \
                 caps=video/x-raw,format=BGRx,width=1707,height=1067,framerate=30/1 ! {}",
                crate::capture::capture_tail()
            ),
            true,
        )?;
        let upload = gst::parse::bin_from_description(&cand.upload_description(height), true)?;
        let enc = cand.make(&encoder::Settings {
            bitrate_kbps: 2000,
            fps: 30,
            keyframe_secs: 1,
        })?;
        let caps = gst::ElementFactory::make("capsfilter")
            .property("caps", cand.output_caps())
            .build()?;
        let parse = gst::ElementFactory::make("h264parse").build()?;
        let sink = gst::ElementFactory::make("appsink").build()?;
        pipeline.add_many([
            src.upcast_ref(),
            upload.upcast_ref(),
            &enc,
            &caps,
            &parse,
            &sink,
        ])?;
        gst::Element::link_many([
            src.upcast_ref(),
            upload.upcast_ref(),
            &enc,
            &caps,
            &parse,
            &sink,
        ])?;
        lock_size(
            &upload.static_pad("sink").unwrap(),
            upload.by_name("size").unwrap(),
            height,
        );

        let appsrc = src
            .by_name("src")
            .unwrap()
            .downcast::<gst_app::AppSrc>()
            .unwrap();
        let appsink = sink.downcast::<gst_app::AppSink>().unwrap();
        pipeline.set_state(gst::State::Playing)?;
        for i in 0..10u64 {
            let mut buf = gst::Buffer::with_size(1707 * 1067 * 4)?;
            buf.get_mut()
                .unwrap()
                .set_pts(gst::ClockTime::from_mseconds(i * 33));
            let _ = appsrc.push_buffer(buf);
        }
        let sample = appsink.try_pull_sample(gst::ClockTime::from_seconds(10));
        let error = pipeline
            .bus()
            .unwrap()
            .pop_filtered(&[gst::MessageType::Error]);
        let _ = pipeline.set_state(gst::State::Null);
        if let Some(msg) = error
            && let gst::MessageView::Error(e) = msg.view()
        {
            anyhow::bail!("{}", e.error());
        }
        let caps = sample
            .ok_or_else(|| anyhow::anyhow!("no frame"))?
            .caps()
            .unwrap()
            .to_owned();
        let st = caps.structure(0).unwrap();
        Ok((st.get("width")?, st.get("height")?))
    }

    #[test]
    fn odd_source_sizes_encode() {
        crate::init().unwrap();
        let mut tried = 0;
        for name in ["x264enc", "openh264enc", "vah264enc", "nvh264enc"] {
            let Some(cand) = Candidate::by_factory(name).filter(|c| c.probe().is_ok()) else {
                continue;
            };
            tried += 1;
            for (height, want) in [
                (None, (1706, 1066)),
                (Some(720), (1152, 720)),
                (Some(1080), (1706, 1066)),
            ] {
                let got =
                    encode(cand, height).unwrap_or_else(|e| panic!("{name} at {height:?}: {e:#}"));
                assert_eq!(got, want, "{name} at {height:?}");
            }
        }
        assert!(tried > 0, "no H.264 encoder to test with");
    }
}
