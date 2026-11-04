//! Capture and encoding decoupled, the way OBS does it.
//!
//! The capture runs in a pipeline of its own that ends in an appsink
//! keeping only the newest frame and never syncing to a clock, so nothing
//! on the encoding side (queues, the low-quality branch, whipclientsink's
//! clock sync, latency changes) can hold the compositor's few buffers or
//! block the capture thread. A frame clock pushes the newest frame into the
//! publishing pipeline at the stream's frame rate, with fresh timestamps,
//! repeating the last one while the screen does not change: the encoders
//! get a constant frame rate, and a viewer's keyframe request is answered
//! within a frame even on a still screen.

use anyhow::{Context, anyhow};
use gst::prelude::*;
use std::sync::atomic::{AtomicBool, AtomicU32, Ordering};
use std::sync::{Arc, Mutex};
use std::thread::JoinHandle;
use std::time::{Duration, Instant};

/// Called with the caps of the first frame of a new capture, before that
/// frame is sent on.
type OnFirst = Box<dyn FnOnce(&gst::CapsRef) + Send>;

/// Where the frame clock takes frames from.
struct Input {
    latest: gst_app::AppSink,
    on_first: Option<OnFirst>,
}

pub(crate) struct FrameClock {
    capture: Mutex<gst::Pipeline>,
    input: Arc<Mutex<Input>>,
    /// The publishing pipeline's video source.
    pub(crate) source: gst_app::AppSrc,
    copy: Option<String>,
    caps: gst::Caps,
    bus: gst::Bus,
    fps: Arc<AtomicU32>,
    stop: Arc<AtomicBool>,
    thread: Mutex<Option<JoinHandle<()>>>,
}

/// The capture's own pipeline: the bin, `copy` when given, and the appsink
/// keeping the newest frame. Its errors are posted on `bus` once `forward`
/// is set.
fn capture_pipeline(
    capture_bin: &gst::Bin,
    copy: Option<&str>,
    caps: &gst::Caps,
    bus: gst::Bus,
    forward: Arc<AtomicBool>,
) -> anyhow::Result<(gst::Pipeline, gst_app::AppSink)> {
    let capture = gst::Pipeline::with_name("cupstream-capture");
    let latest = gst_app::AppSink::builder()
        .name("latest")
        .caps(caps)
        .sync(false)
        .max_buffers(1)
        .drop(true)
        .build();
    capture.add_many([
        capture_bin.upcast_ref(),
        latest.upcast_ref::<gst::Element>(),
    ])?;
    match copy {
        Some(desc) => {
            let copy = gst::parse::bin_from_description(desc, true)?;
            capture.add(&copy)?;
            gst::Element::link_many([
                capture_bin.upcast_ref::<gst::Element>(),
                copy.upcast_ref(),
                latest.upcast_ref(),
            ])
            .context("linking the capture")?;
        }
        None => capture_bin.link(&latest).context("linking the capture")?,
    }
    capture
        .bus()
        .context("capture bus")?
        .set_sync_handler(move |_, msg| {
            if let gst::MessageView::Error(_) = msg.view()
                && forward.load(Ordering::Relaxed)
            {
                let _ = bus.post(msg.clone());
            }
            gst::BusSyncReply::Drop
        });
    Ok((capture, latest))
}

/// Starts a capture pipeline, posting its errors only once it started:
/// a capture that fails to start is reported by the caller.
fn start_capture(capture: &gst::Pipeline, forward: &AtomicBool) -> anyhow::Result<()> {
    if capture.set_state(gst::State::Playing).is_err() {
        stop_capture(capture);
        return Err(anyhow!("the capture did not start"));
    }
    forward.store(true, Ordering::Relaxed);
    Ok(())
}

/// Off the caller's thread, which may be inside an async runtime.
fn stop_capture(capture: &gst::Pipeline) {
    std::thread::scope(|s| {
        s.spawn(|| {
            let _ = capture.set_state(gst::State::Null);
        });
    });
}

impl FrameClock {
    /// Puts the capture bin into a pipeline of its own, followed by `copy`
    /// when given (see `Candidate::capture_copy_description`). `caps` are the
    /// memory the encoder takes (see `Candidate::capture_caps`), so the
    /// capture negotiates DMA-BUF or shared memory as it would feeding the
    /// encoder directly. Errors of the capture are posted on `bus`, the
    /// publishing pipeline's.
    pub(crate) fn new(
        capture_bin: &gst::Bin,
        copy: Option<&str>,
        caps: gst::Caps,
        fps: u32,
        bus: gst::Bus,
    ) -> anyhow::Result<Self> {
        let forward = Arc::new(AtomicBool::new(true));
        let (capture, latest) = capture_pipeline(capture_bin, copy, &caps, bus.clone(), forward)?;

        let source = gst_app::AppSrc::builder()
            .name("frames")
            .is_live(true)
            .format(gst::Format::Time)
            .do_timestamp(false)
            .max_buffers(2)
            .build();
        // Should the encoding side fall behind, drop the oldest frame.
        source.set_property_from_str("leaky-type", "downstream");
        Ok(Self {
            capture: Mutex::new(capture),
            input: Arc::new(Mutex::new(Input {
                latest,
                on_first: None,
            })),
            source,
            copy: copy.map(str::to_owned),
            caps,
            bus,
            fps: Arc::new(AtomicU32::new(fps.max(1))),
            stop: Arc::default(),
            thread: Mutex::default(),
        })
    }

    /// Starts the capture and the frame clock; `publish` is already playing.
    pub(crate) fn start(&self, publish: &gst::Pipeline) -> anyhow::Result<()> {
        self.capture
            .lock()
            .unwrap()
            .set_state(gst::State::Playing)
            .map_err(|_| anyhow!("the capture did not start"))?;
        let (input, source, stop, fps) = (
            self.input.clone(),
            self.source.clone(),
            self.stop.clone(),
            self.fps.clone(),
        );
        let publish = publish.downgrade();
        let thread = std::thread::Builder::new()
            .name("frame-clock".into())
            .spawn(move || {
                let mut next = Instant::now();
                let mut last: Option<gst::Sample> = None;
                let mut caps: Option<gst::Caps> = None;
                while !stop.load(Ordering::Relaxed) {
                    let interval = Duration::from_nanos(
                        1_000_000_000 / u64::from(fps.load(Ordering::Relaxed)),
                    );
                    {
                        let mut input = input.lock().unwrap();
                        if let Some(sample) = input.latest.try_pull_sample(gst::ClockTime::ZERO) {
                            if let Some(c) = sample.caps()
                                && let Some(f) = input.on_first.take()
                            {
                                f(c);
                            }
                            last = Some(sample);
                        }
                    }
                    let running = publish.upgrade().and_then(|p| {
                        let now = p.clock()?.time();
                        now.checked_sub(p.base_time()?)
                    });
                    if let (Some(sample), Some(running)) = (&last, running)
                        && let Some(buffer) = sample.buffer()
                    {
                        if let Some(c) = sample.caps().map(|c| c.to_owned())
                            && caps.as_ref() != Some(&c)
                        {
                            source.set_caps(Some(&c));
                            caps = Some(c);
                        }
                        // A new buffer sharing the frame's memory.
                        let mut out = buffer.copy();
                        {
                            let out = out.make_mut();
                            out.set_pts(running);
                            out.set_dts(gst::ClockTime::NONE);
                            out.set_duration(gst::ClockTime::from_nseconds(
                                interval.as_nanos() as u64
                            ));
                        }
                        if let Err(gst::FlowError::Flushing | gst::FlowError::Eos) =
                            source.push_buffer(out)
                        {
                            break;
                        }
                    }
                    next += interval;
                    let now = Instant::now();
                    if next > now {
                        std::thread::sleep(next - now);
                    } else if now - next > interval {
                        // Fell behind (the machine was busy): skip ahead.
                        next = now;
                    }
                }
            })?;
        *self.thread.lock().unwrap() = Some(thread);
        Ok(())
    }

    /// Captures from another bin from now on. The new capture starts
    /// first; until its first frame arrives the last frame of the old one
    /// is repeated, so the stream never pauses. `on_first` gets the caps of
    /// that first frame before it is sent on. The old capture is stopped
    /// before this returns, so whatever it needs (the portal session) can
    /// go then.
    pub(crate) fn switch(
        &self,
        capture_bin: &gst::Bin,
        on_first: impl FnOnce(&gst::CapsRef) + Send + 'static,
    ) -> anyhow::Result<()> {
        let forward = Arc::new(AtomicBool::new(false));
        let (capture, latest) = capture_pipeline(
            capture_bin,
            self.copy.as_deref(),
            &self.caps,
            self.bus.clone(),
            forward.clone(),
        )?;
        start_capture(&capture, &forward)?;
        *self.input.lock().unwrap() = Input {
            latest,
            on_first: Some(Box::new(on_first)),
        };
        let old = std::mem::replace(&mut *self.capture.lock().unwrap(), capture);
        stop_capture(&old);
        Ok(())
    }

    /// Changes the frame rate frames are sent on at.
    pub(crate) fn set_fps(&self, fps: u32) {
        self.fps.store(fps.max(1), Ordering::Relaxed);
    }

    pub(crate) fn stop(&self) {
        self.stop.store(true, Ordering::Relaxed);
        if let Some(t) = self.thread.lock().unwrap().take() {
            let _ = t.join();
        }
        stop_capture(&self.capture.lock().unwrap());
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::atomic::AtomicU32;

    /// A capture at `capture_fps` through the frame clock at 60 fps into a
    /// publishing side that takes `encode_ms` per frame, for two seconds:
    /// (frames captured, frames published).
    fn run(capture_fps: u32, encode_ms: u64) -> (u32, u32) {
        crate::init().unwrap();
        let capture_bin = gst::parse::bin_from_description(
            &format!("videotestsrc is-live=true ! capsfilter caps=video/x-raw,format=I420,width=64,height=64,framerate={capture_fps}/1"),
            true,
        )
        .unwrap();
        let captured = Arc::new(AtomicU32::new(0));
        let c = captured.clone();
        capture_bin
            .static_pad("src")
            .unwrap()
            .add_probe(gst::PadProbeType::BUFFER, move |_, _| {
                c.fetch_add(1, Ordering::Relaxed);
                gst::PadProbeReturn::Ok
            });

        let publish = gst::Pipeline::new();
        let frames = FrameClock::new(
            &capture_bin,
            None,
            "video/x-raw".parse().unwrap(),
            60,
            publish.bus().unwrap(),
        )
        .unwrap();
        // identity sleep-time: an encoder that needs encode_ms per frame.
        let slow = gst::ElementFactory::make("identity")
            .property("sleep-time", (encode_ms * 1000) as u32)
            .build()
            .unwrap();
        let sink = gst::ElementFactory::make("fakesink")
            .property("sync", false)
            .build()
            .unwrap();
        publish
            .add_many([frames.source.upcast_ref(), &slow, &sink])
            .unwrap();
        gst::Element::link_many([frames.source.upcast_ref(), &slow, &sink]).unwrap();
        let published = Arc::new(AtomicU32::new(0));
        let p = published.clone();
        sink.static_pad("sink")
            .unwrap()
            .add_probe(gst::PadProbeType::BUFFER, move |_, _| {
                p.fetch_add(1, Ordering::Relaxed);
                gst::PadProbeReturn::Ok
            });

        publish.set_state(gst::State::Playing).unwrap();
        frames.start(&publish).unwrap();
        std::thread::sleep(Duration::from_secs(2));
        frames.stop();
        let _ = publish.set_state(gst::State::Null);
        (
            captured.load(Ordering::Relaxed),
            published.load(Ordering::Relaxed),
        )
    }

    #[test]
    fn a_slow_encoder_does_not_slow_the_capture() {
        // 50 ms per frame: about 20 frames a second get through, while the
        // capture keeps its 120 fps (the compositor gets its buffers back).
        let (captured, published) = run(120, 50);
        assert!(captured >= 180, "captured {captured} in 2 s at 120 fps");
        assert!((25..=50).contains(&published), "published {published}");
    }

    #[test]
    fn switching_the_capture_keeps_the_frames_coming() {
        crate::init().unwrap();
        let test_source = |size: &str| {
            gst::parse::bin_from_description(
                &format!("videotestsrc is-live=true ! capsfilter caps=video/x-raw,format=I420,{size},framerate=30/1"),
                true,
            )
            .unwrap()
        };
        let publish = gst::Pipeline::new();
        let frames = FrameClock::new(
            &test_source("width=64,height=64"),
            None,
            "video/x-raw".parse().unwrap(),
            60,
            publish.bus().unwrap(),
        )
        .unwrap();
        let sink = gst_app::AppSink::builder().sync(false).build();
        publish
            .add_many([
                frames.source.upcast_ref::<gst::Element>(),
                sink.upcast_ref(),
            ])
            .unwrap();
        frames.source.link(&sink).unwrap();
        let widths = Arc::new(Mutex::new(Vec::<i32>::new()));
        let w = widths.clone();
        sink.static_pad("sink")
            .unwrap()
            .add_probe(gst::PadProbeType::BUFFER, move |pad, _| {
                let width = pad
                    .current_caps()
                    .and_then(|c| c.structure(0)?.get::<i32>("width").ok())
                    .unwrap_or_default();
                w.lock().unwrap().push(width);
                gst::PadProbeReturn::Ok
            });

        publish.set_state(gst::State::Playing).unwrap();
        frames.start(&publish).unwrap();
        std::thread::sleep(Duration::from_millis(500));
        let first = Arc::new(Mutex::new(None::<gst::Caps>));
        let f = first.clone();
        frames
            .switch(&test_source("width=128,height=96"), move |caps| {
                *f.lock().unwrap() = Some(caps.to_owned());
            })
            .unwrap();
        frames.set_fps(30);
        std::thread::sleep(Duration::from_millis(1000));
        frames.stop();
        let _ = publish.set_state(gst::State::Null);

        let first = first
            .lock()
            .unwrap()
            .clone()
            .expect("the new capture's first frame");
        assert_eq!(first.structure(0).unwrap().get::<i32>("width"), Ok(128));
        let widths = widths.lock().unwrap();
        let before = widths.iter().filter(|&&w| w == 64).count();
        let after = widths.iter().filter(|&&w| w == 128).count();
        // 60 fps before the switch, 30 after; the old source until the
        // switch, then only the new one.
        assert!((20..=35).contains(&before), "{before} frames before");
        assert!((22..=35).contains(&after), "{after} frames after");
        assert!(widths.iter().skip_while(|&&w| w == 64).all(|&w| w == 128));
    }

    #[test]
    fn a_slow_capture_still_gives_a_constant_frame_rate() {
        // A still screen sends few frames; the last one is repeated at 60 fps.
        let (captured, published) = run(10, 0);
        assert!(captured <= 25, "captured {captured}");
        assert!(
            (90..=125).contains(&published),
            "published {published} in 2 s at 60 fps"
        );
    }
}
