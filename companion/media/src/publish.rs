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
    _capture: Capture,
    audio: Option<AudioCapture>,
    low: Option<LowCtl>,
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
        pipeline.add_many([
            capture.bin.upcast_ref(),
            &video_tee,
            &queue,
            upload.upcast_ref(),
            &enc,
            &caps,
            &parse,
        ])?;
        gst::Element::link_many([
            capture.bin.upcast_ref(),
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
            _capture: capture,
            audio,
            low,
        })
    }

    pub fn start(&self) -> anyhow::Result<()> {
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

/// whipclientsink cannot renegotiate, so the encoded caps must never
/// change. Some parsers (h264parse in GStreamer 1.24) first pass the
/// encoder's caps on and add these fields once they parsed the SPS; they
/// are dropped so both caps are the same.
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
    pad.add_probe(gst::PadProbeType::EVENT_DOWNSTREAM, |_, info| {
        if let Some(gst::PadProbeData::Event(ev)) = &mut info.data
            && let gst::EventView::Caps(c) = ev.view()
        {
            let mut caps = c.caps().to_owned();
            for s in caps.make_mut().iter_mut() {
                s.remove_fields(ADDED_LATER);
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

/// Fixes the encoded size from the first frame's: the capsfilter `size`
/// after the converter gets the size [`output_size`] computes, before the
/// converter negotiates. Later, when a captured window is resized, its
/// frames are scaled (with borders) to the same size rather than changing
/// the stream's caps, which whipclientsink cannot renegotiate. `input` is
/// the converter's sink.
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
        let dims = c
            .caps()
            .structure(0)
            .and_then(|s| Some((s.get::<i32>("width").ok()?, s.get::<i32>("height").ok()?)));
        // A frame without a usable size cannot fix it; wait for the next.
        let Some((w, h)) = dims.filter(|(w, h)| *w > 0 && *h > 0) else {
            return gst::PadProbeReturn::Ok;
        };
        let (w, h) = output_size(w as u32, h as u32, target_height);
        let mut caps = size.property::<gst::Caps>("caps");
        if let Some(st) = caps.make_mut().structure_mut(0) {
            st.set("width", w as i32);
            st.set("height", h as i32);
        }
        tracing::debug!("encoding {w}x{h}");
        size.set_property("caps", caps);
        gst::PadProbeReturn::Remove
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
