//! Watching a stream over WHEP and counting decoded frames: the check that
//! what the app publishes actually plays (cupstream-cli watch, and pipeline
//! tests).

use anyhow::{anyhow, bail};
use gst::glib;
use gst::prelude::*;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

/// What a viewer received.
#[derive(Debug, Default)]
pub struct WatchReport {
    pub video_frames: u64,
    pub audio_buffers: u64,
    /// From connecting to the first decoded video frame.
    pub first_frame: Option<Duration>,
    pub width: i32,
    pub height: i32,
}

/// Plays a WHEP endpoint for `duration` and reports what was decoded.
pub fn watch(url: &str, token: Option<&str>, duration: Duration) -> anyhow::Result<WatchReport> {
    let pipeline = gst::Pipeline::new();
    let src = gst::ElementFactory::make("whepclientsrc").build()?;
    let signaller = src.property::<glib::Object>("signaller");
    signaller.set_property("whep-endpoint", url);
    if let Some(t) = token {
        signaller.set_property("auth-token", t);
    }
    src.set_property("stun-server", None::<String>);
    pipeline.add(&src)?;

    let video = Arc::new(AtomicU64::new(0));
    let audio = Arc::new(AtomicU64::new(0));
    let first: Arc<Mutex<Option<Duration>>> = Arc::default();
    let size: Arc<Mutex<(i32, i32)>> = Arc::default();
    let started = Instant::now();

    let weak = pipeline.downgrade();
    let (v, a, f, sz) = (video.clone(), audio.clone(), first.clone(), size.clone());
    src.connect_pad_added(move |_, pad| {
        let Some(pipeline) = weak.upgrade() else {
            return;
        };
        let is_video = pad.name().starts_with("video");
        // whepclientsrc may hand out encoded media; decode it either way.
        let decode = gst::ElementFactory::make("decodebin")
            .build()
            .expect("decodebin");
        pipeline.add(&decode).expect("add decodebin");
        decode.sync_state_with_parent().ok();
        pad.link(&decode.static_pad("sink").expect("sink pad")).ok();
        let (v, a, f, sz) = (v.clone(), a.clone(), f.clone(), sz.clone());
        let weak = pipeline.downgrade();
        decode.connect_pad_added(move |_, pad| {
            let Some(pipeline) = weak.upgrade() else {
                return;
            };
            let sink = gst::ElementFactory::make("fakesink")
                .property("sync", false)
                .build()
                .expect("fakesink");
            pipeline.add(&sink).expect("add fakesink");
            sink.sync_state_with_parent().ok();
            pad.link(&sink.static_pad("sink").expect("sink pad")).ok();
            let (v, a, f, sz) = (v.clone(), a.clone(), f.clone(), sz.clone());
            pad.add_probe(gst::PadProbeType::BUFFER, move |pad, _| {
                if is_video {
                    if v.fetch_add(1, Ordering::Relaxed) == 0 {
                        *f.lock().unwrap() = Some(started.elapsed());
                        if let Some(info) = pad
                            .current_caps()
                            .and_then(|c| gst_video::VideoInfo::from_caps(&c).ok())
                        {
                            *sz.lock().unwrap() = (info.width() as i32, info.height() as i32);
                        }
                    }
                } else {
                    a.fetch_add(1, Ordering::Relaxed);
                }
                gst::PadProbeReturn::Ok
            });
        });
    });

    pipeline.set_state(gst::State::Playing)?;
    let bus = pipeline.bus().expect("pipelines have a bus");
    let deadline = Instant::now() + duration;
    let result = loop {
        let left = deadline.saturating_duration_since(Instant::now());
        if left.is_zero() {
            break Ok(());
        }
        let Some(msg) = bus.timed_pop_filtered(
            gst::ClockTime::from_mseconds(left.as_millis().min(200) as u64),
            &[gst::MessageType::Error, gst::MessageType::Eos],
        ) else {
            continue;
        };
        match msg.view() {
            gst::MessageView::Error(e) => break Err(anyhow!("{}", e.error())),
            _ => break Ok(()),
        }
    };
    let _ = pipeline.set_state(gst::State::Null);
    result?;
    let (width, height) = *size.lock().unwrap();
    let report = WatchReport {
        video_frames: video.load(Ordering::Relaxed),
        audio_buffers: audio.load(Ordering::Relaxed),
        first_frame: *first.lock().unwrap(),
        width,
        height,
    };
    if report.video_frames == 0 {
        bail!("no video frame was decoded ({report:?})");
    }
    Ok(report)
}
