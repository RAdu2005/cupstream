//! The low-quality version of a stream (overview 7.3): a second, smaller
//! encode of the same capture, published as its own WHIP session for
//! viewers on slow connections.
//!
//! It hangs off tees after the capture and after the Opus encoder, and is
//! added only once the stream's own session is connected (the server
//! refuses it before). It must never cost the stream itself: its queue
//! holds one frame and drops the rest when the encoder falls behind, and
//! an error in it removes only it. It is then tried again, with the next
//! encoder when the encoder was at fault (consumer GPUs limit how many
//! sessions they encode at once).

use anyhow::Context;
use gst::glib;
use gst::prelude::*;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use crate::encoder::{self, Candidate, Codec};
use crate::publish::{Endpoint, keep_caps_stable, keyframes_until_connected, lock_size};

/// Attempts after a failure before giving up on the low-quality version.
const MAX_ATTEMPTS: u32 = 5;

/// What the low-quality version is encoded as.
#[derive(Clone, Debug)]
pub struct LowSettings {
    pub endpoint: Endpoint,
    pub height: u32,
    pub fps: u32,
    pub bitrate_kbps: u32,
}

impl LowSettings {
    /// About 720p at 30 fps and 1.2 Mbit/s, or 480p at 0.8 Mbit/s when the
    /// stream itself is 720p or smaller: readable screen text at a bitrate
    /// slow connections keep up with.
    pub fn for_stream(endpoint: Endpoint, stream_height: Option<u32>) -> Self {
        let small = stream_height.is_some_and(|h| h <= 720);
        Self {
            endpoint,
            height: if small { 480 } else { 720 },
            fps: 30,
            bitrate_kbps: if small { 800 } else { 1200 },
        }
    }
}

/// Where the low-quality version stands, for the app to show.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum LowState {
    /// Waiting for the stream's own session to connect.
    Waiting,
    /// Publishing, with this encoder.
    Live { encoder: String },
    /// Failed for now; trying again.
    Retrying { reason: String },
    /// Given up after repeated failures.
    Failed { reason: String },
}

struct Branch {
    elements: Vec<gst::Element>,
    /// The tees' request pads feeding it.
    pads: Vec<gst::Pad>,
}

struct Inner {
    candidates: Vec<&'static Candidate>,
    next: usize,
    attempts: u32,
    branch: Option<Branch>,
    state: LowState,
    stopped: bool,
}

/// Adds, removes and retries the low-quality branch of a pipeline.
#[derive(Clone)]
pub(crate) struct LowCtl {
    pipeline: glib::WeakRef<gst::Pipeline>,
    video_tee: gst::Element,
    audio_tee: Option<gst::Element>,
    settings: Arc<LowSettings>,
    inner: Arc<Mutex<Inner>>,
}

/// Encoders for the low-quality version: the stream's own when it is a
/// hardware H.264 encoder (a second session on the GPU costs the CPU
/// nothing), then OpenH264 and x264, which are cheap at this size.
fn candidates(main: &'static Candidate) -> Vec<&'static Candidate> {
    let mut list = Vec::new();
    if main.hardware && main.codec == Codec::H264 {
        list.push(main);
    }
    for name in ["openh264enc", "x264enc"] {
        if let Some(c) = Candidate::by_factory(name)
            && c.installed()
        {
            list.push(c);
        }
    }
    list
}

impl LowCtl {
    pub(crate) fn new(
        pipeline: &gst::Pipeline,
        video_tee: gst::Element,
        audio_tee: Option<gst::Element>,
        main: &'static Candidate,
        settings: LowSettings,
    ) -> Self {
        Self {
            pipeline: pipeline.downgrade(),
            video_tee,
            audio_tee,
            settings: Arc::new(settings),
            inner: Arc::new(Mutex::new(Inner {
                candidates: candidates(main),
                next: 0,
                attempts: 0,
                branch: None,
                state: LowState::Waiting,
                stopped: false,
            })),
        }
    }

    pub(crate) fn state(&self) -> LowState {
        self.inner.lock().unwrap().state.clone()
    }

    pub(crate) fn stop(&self) {
        self.inner.lock().unwrap().stopped = true;
    }

    /// Adds the branch once the stream's own WebRTC connection is up.
    pub(crate) fn start_when_connected(&self, main_sink: &gst::Element) {
        let ctl = self.clone();
        main_sink.connect("consumer-added", false, move |args| {
            let webrtcbin = args.get(2).and_then(|v| v.get::<gst::Element>().ok())?;
            let ctl = ctl.clone();
            let started = Arc::new(std::sync::atomic::AtomicBool::new(false));
            webrtcbin.connect_notify(Some("connection-state"), move |w, _| {
                let state = w.property::<gst_webrtc::WebRTCPeerConnectionState>("connection-state");
                if state == gst_webrtc::WebRTCPeerConnectionState::Connected
                    && !started.swap(true, std::sync::atomic::Ordering::SeqCst)
                {
                    let ctl = ctl.clone();
                    // Not on the streaming thread that notified us.
                    std::thread::spawn(move || ctl.attach());
                }
            });
            None
        });
    }

    fn attach(&self) {
        let (cand, attempt) = {
            let inner = self.inner.lock().unwrap();
            if inner.stopped || inner.branch.is_some() {
                return;
            }
            let Some(&cand) = inner.candidates.get(inner.next) else {
                drop(inner);
                self.give_up("no encoder left to try".into());
                return;
            };
            (cand, inner.attempts)
        };
        match self.build(cand) {
            Ok(branch) => {
                tracing::info!(
                    "low-quality version: publishing with {} (attempt {})",
                    cand.factory,
                    attempt + 1
                );
                let mut inner = self.inner.lock().unwrap();
                inner.branch = Some(branch);
                inner.state = LowState::Live {
                    encoder: format!("{} ({})", cand.label, cand.factory),
                };
            }
            Err(e) => self.failed(&format!("{e:#}"), true),
        }
    }

    fn build(&self, cand: &'static Candidate) -> anyhow::Result<Branch> {
        let pipeline = self.pipeline.upgrade().context("the stream has stopped")?;
        let s = &self.settings;
        let video = gst::parse::bin_from_description(
            &format!(
                "queue leaky=downstream max-size-buffers=1 max-size-time=0 max-size-bytes=0 ! {}",
                cand.upload_description(Some(s.height))
            ),
            true,
        )?;
        video.set_property("name", "low-video");
        if let Some(pad) = video.static_pad("sink") {
            crate::capture::limit_frame_rate(&pad, s.fps);
        }
        let enc = cand.make(&encoder::Settings {
            bitrate_kbps: s.bitrate_kbps,
            fps: s.fps,
            keyframe_secs: 4,
        })?;
        enc.set_property("name", "low-enc");
        let caps = gst::ElementFactory::make("capsfilter")
            .name("low-caps")
            .property("caps", cand.output_caps())
            .build()?;
        let parse = gst::ElementFactory::make("h264parse")
            .name("low-parse")
            .property("config-interval", -1i32)
            .build()?;
        let sink = gst::ElementFactory::make("whipclientsink")
            .name("whip-low")
            .build()?;
        let signaller = sink.property::<glib::Object>("signaller");
        signaller.set_property("whip-endpoint", &s.endpoint.url);
        signaller.set_property("auth-token", &s.endpoint.token);
        sink.set_property_from_str("congestion-control", "disabled");
        sink.set_property("stun-server", None::<String>);

        let mut elements: Vec<gst::Element> = vec![
            video.clone().upcast(),
            enc.clone(),
            caps.clone(),
            parse.clone(),
            sink.clone(),
        ];
        let audio_queue = match &self.audio_tee {
            Some(_) => {
                let q = gst::ElementFactory::make("queue")
                    .name("low-audio")
                    .build()?;
                elements.push(q.clone());
                Some(q)
            }
            None => None,
        };
        pipeline.add_many(&elements)?;
        let linked = (|| -> anyhow::Result<Vec<gst::Pad>> {
            gst::Element::link_many([video.upcast_ref(), &enc, &caps, &parse])
                .context("linking the low-quality encoder")?;
            // whipclientsink's pads are asked for by kind: picked by caps,
            // the audio queue (not fed yet) could get a video pad.
            let link_to_sink = |from: &gst::Element, template: &str| -> anyhow::Result<()> {
                let pad = sink
                    .request_pad_simple(template)
                    .with_context(|| format!("whipclientsink {template}"))?;
                from.static_pad("src")
                    .context("source pad")?
                    .link(&pad)
                    .with_context(|| format!("linking to whipclientsink {template}"))?;
                Ok(())
            };
            link_to_sink(&parse, "video_%u")?;
            if let Some(q) = &audio_queue {
                link_to_sink(q, "audio_%u")?;
            }
            keep_caps_stable(&parse);
            if let (Some(size), Some(input)) = (video.by_name("size"), video.static_pad("sink")) {
                lock_size(&input, size, Some(s.height));
            }
            for e in &elements {
                e.sync_state_with_parent()?;
            }
            // Feed it only once it is playing.
            let mut pads = Vec::new();
            let vpad = self
                .video_tee
                .request_pad_simple("src_%u")
                .context("video tee pad")?;
            vpad.link(&video.static_pad("sink").context("low video sink pad")?)
                .context("linking the video tee")?;
            pads.push(vpad);
            if let (Some(tee), Some(q)) = (&self.audio_tee, &audio_queue) {
                let apad = tee.request_pad_simple("src_%u").context("audio tee pad")?;
                apad.link(&q.static_pad("sink").context("low audio sink pad")?)
                    .context("linking the audio tee")?;
                pads.push(apad);
            }
            Ok(pads)
        })();
        match linked {
            Ok(pads) => {
                keyframes_until_connected(&sink, &enc);
                Ok(Branch { elements, pads })
            }
            Err(e) => {
                detach(
                    &pipeline,
                    Branch {
                        elements,
                        pads: vec![],
                    },
                );
                Err(e)
            }
        }
    }

    /// Handles an error message: true when it came from the low-quality
    /// branch (which is then removed and tried again), false when it
    /// concerns the stream itself.
    pub(crate) fn handle_error(&self, msg: &gst::Message) -> bool {
        let Some(src) = msg.src() else { return false };
        let mut obj: Option<gst::Object> = Some(src.clone());
        let mut in_low = false;
        let mut from_encoder = false;
        while let Some(o) = obj {
            let name = o.name();
            if name == "low-enc" {
                from_encoder = true;
            }
            if name.starts_with("low-") || name == "whip-low" {
                in_low = true;
            }
            obj = o.parent();
        }
        if !in_low {
            return false;
        }
        let reason = match msg.view() {
            gst::MessageView::Error(e) => e.error().to_string(),
            _ => "error".into(),
        };
        // A failure before the encoder produced anything is the encoder's
        // (e.g. a GPU that refuses another session); move on to the next.
        self.failed(&reason, from_encoder);
        true
    }

    fn failed(&self, reason: &str, next_encoder: bool) {
        let (branch, attempts) = {
            let mut inner = self.inner.lock().unwrap();
            if next_encoder {
                inner.next += 1;
            }
            inner.attempts += 1;
            (inner.branch.take(), inner.attempts)
        };
        tracing::warn!("low-quality version failed ({reason}); the stream itself continues");
        if let (Some(b), Some(p)) = (branch, self.pipeline.upgrade()) {
            detach(&p, b);
        }
        if attempts >= MAX_ATTEMPTS {
            self.give_up(reason.into());
            return;
        }
        self.inner.lock().unwrap().state = LowState::Retrying {
            reason: reason.into(),
        };
        let ctl = self.clone();
        std::thread::spawn(move || {
            std::thread::sleep(Duration::from_secs(2 * attempts as u64));
            ctl.attach();
        });
    }

    fn give_up(&self, reason: String) {
        tracing::warn!("low-quality version given up: {reason}");
        self.inner.lock().unwrap().state = LowState::Failed { reason };
    }
}

/// Unlinks a branch from the tees and removes it, off the streaming
/// threads; stopping its whipclientsink ends the low-quality session.
fn detach(pipeline: &gst::Pipeline, branch: Branch) {
    for pad in &branch.pads {
        if let Some(peer) = pad.peer() {
            let _ = pad.unlink(&peer);
        }
        if let Some(tee) = pad.parent_element() {
            tee.release_request_pad(pad);
        }
    }
    let pipeline = pipeline.clone();
    std::thread::spawn(move || {
        for e in &branch.elements {
            let _ = e.set_state(gst::State::Null);
        }
        let _ = pipeline.remove_many(&branch.elements);
    });
}
