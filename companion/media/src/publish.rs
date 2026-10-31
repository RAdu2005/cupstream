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
}

impl Publisher {
    /// Builds the pipeline; `start` sets it playing.
    pub fn new(
        capture: Capture,
        audio: Option<AudioCapture>,
        encoder_info: &'static Candidate,
        video: VideoSettings,
        endpoint: &Endpoint,
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
        pipeline.add_many([
            capture.bin.upcast_ref(),
            &queue,
            upload.upcast_ref(),
            &enc,
            &caps,
            &parse,
        ])?;
        gst::Element::link_many([
            capture.bin.upcast_ref(),
            &queue,
            upload.upcast_ref(),
            &enc,
            &caps,
            &parse,
            &sink,
        ])?;
        log_keyframe_requests(&enc);
        keep_caps_stable(&parse);
        if let Some(size) = upload.by_name("size") {
            lock_size(&enc, size);
        }

        if let Some(audio) = &audio {
            let queue = gst::ElementFactory::make("queue").build()?;
            let opus = gst::ElementFactory::make("opusenc")
                .property("bitrate", AUDIO_BITRATE)
                .property("inband-fec", true)
                .property_from_str("frame-size", "10")
                .build()?;
            pipeline.add_many([audio.bin.upcast_ref(), &queue, &opus])?;
            gst::Element::link_many([audio.bin.upcast_ref(), &queue, &opus, &sink])?;
        }

        Ok(Self {
            pipeline,
            encoder: enc,
            encoder_info,
            _capture: capture,
            audio,
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
        self.keyframes_until_connected();
        Ok(())
    }

    /// Before the WHIP session exists, whipclientsink inspects the stream
    /// and waits for a keyframe, which with a long keyframe interval takes
    /// seconds. Ask for one every 250 ms until the session is up and
    /// MediaMTX asks itself.
    fn keyframes_until_connected(&self) {
        let connected = std::sync::Arc::new(std::sync::atomic::AtomicBool::new(false));
        if let Some(sink) = self.pipeline.by_name("whip") {
            let c = connected.clone();
            sink.connect("consumer-added", false, move |_| {
                c.store(true, std::sync::atomic::Ordering::Relaxed);
                None
            });
        }
        let Some(pad) = self.encoder.static_pad("src") else {
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

/// whipclientsink cannot renegotiate, so the encoded caps must never
/// change. Some parsers (h264parse in GStreamer 1.24) first pass the
/// encoder's caps on and add these fields once they parsed the SPS; they
/// are dropped so both caps are the same.
fn keep_caps_stable(parse: &gst::Element) {
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

/// Fixes the encoded size to the first frame's: when a captured window is
/// resized, later frames are scaled (with borders) to the same size rather
/// than changing the stream's caps.
fn lock_size(enc: &gst::Element, size: gst::Element) {
    let Some(pad) = enc.static_pad("sink") else {
        return;
    };
    let size = size.downgrade();
    pad.add_probe(gst::PadProbeType::EVENT_DOWNSTREAM, move |_, info| {
        let Some(gst::PadProbeData::Event(ev)) = &info.data else {
            return gst::PadProbeReturn::Ok;
        };
        let gst::EventView::Caps(c) = ev.view() else {
            return gst::PadProbeReturn::Ok;
        };
        let (Some(size), Some(s)) = (size.upgrade(), c.caps().structure(0)) else {
            return gst::PadProbeReturn::Remove;
        };
        if let (Ok(w), Ok(h)) = (s.get::<i32>("width"), s.get::<i32>("height")) {
            let mut caps = size.property::<gst::Caps>("caps");
            if let Some(st) = caps.make_mut().structure_mut(0) {
                st.set("width", w);
                st.set("height", h);
            }
            size.set_property("caps", caps);
        }
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
