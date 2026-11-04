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
use std::sync::Arc;
use std::sync::atomic::{AtomicBool, Ordering};
use std::thread::JoinHandle;
use std::time::{Duration, Instant};

pub(crate) struct FrameClock {
    capture: gst::Pipeline,
    latest: gst_app::AppSink,
    /// The publishing pipeline's video source.
    pub(crate) source: gst_app::AppSrc,
    fps: u32,
    stop: Arc<AtomicBool>,
    thread: std::sync::Mutex<Option<JoinHandle<()>>>,
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
        let capture = gst::Pipeline::with_name("cupstream-capture");
        let latest = gst_app::AppSink::builder()
            .name("latest")
            .caps(&caps)
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
                if let gst::MessageView::Error(_) = msg.view() {
                    let _ = bus.post(msg.clone());
                }
                gst::BusSyncReply::Drop
            });

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
            capture,
            latest,
            source,
            fps: fps.max(1),
            stop: Arc::default(),
            thread: std::sync::Mutex::default(),
        })
    }

    /// Starts the capture and the frame clock; `publish` is already playing.
    pub(crate) fn start(&self, publish: &gst::Pipeline) -> anyhow::Result<()> {
        self.capture
            .set_state(gst::State::Playing)
            .map_err(|_| anyhow!("the capture did not start"))?;
        let (latest, source, stop) = (self.latest.clone(), self.source.clone(), self.stop.clone());
        let publish = publish.downgrade();
        let interval = Duration::from_nanos(1_000_000_000 / u64::from(self.fps));
        let thread = std::thread::Builder::new()
            .name("frame-clock".into())
            .spawn(move || {
                let mut next = Instant::now();
                let mut last: Option<gst::Sample> = None;
                let mut caps: Option<gst::Caps> = None;
                while !stop.load(Ordering::Relaxed) {
                    if let Some(sample) = latest.try_pull_sample(gst::ClockTime::ZERO) {
                        last = Some(sample);
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

    pub(crate) fn stop(&self) {
        self.stop.store(true, Ordering::Relaxed);
        if let Some(t) = self.thread.lock().unwrap().take() {
            let _ = t.join();
        }
        let capture = &self.capture;
        std::thread::scope(|s| {
            s.spawn(|| {
                let _ = capture.set_state(gst::State::Null);
            });
        });
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
