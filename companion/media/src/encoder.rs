//! Video encoders: the candidates for each platform in order of preference,
//! a probe that checks a candidate really works on this machine (an element
//! can exist while its driver or GPU cannot encode), and the settings that
//! make each one suitable for live streaming (no B-frames, low latency,
//! a bitrate that can change while live).

use anyhow::{Context, anyhow, bail};
use gst::glib;
use gst::prelude::*;
use std::time::Duration;

/// Video codecs viewers can decode without transcoding (overview 3.1):
/// H.264 by default, AV1 optional.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Codec {
    H264,
    Av1,
}

/// How frames reach an encoder: the converter that uploads and converts
/// them, so hardware encoders get GPU memory without a copy through the CPU.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Upload {
    /// videoconvertscale into system memory (software encoders).
    System,
    /// vapostproc into VA memory (VA-API).
    Va,
    /// cudaupload and cudaconvertscale (NVENC on Linux).
    Cuda,
    /// d3d11upload and d3d11convert (Windows hardware encoders).
    D3d11,
}

/// One encoder element.
#[derive(Clone, Copy, Debug)]
pub struct Candidate {
    pub factory: &'static str,
    pub label: &'static str,
    pub codec: Codec,
    pub hardware: bool,
    pub upload: Upload,
}

const fn c(
    factory: &'static str,
    label: &'static str,
    codec: Codec,
    hardware: bool,
    upload: Upload,
) -> Candidate {
    Candidate {
        factory,
        label,
        codec,
        hardware,
        upload,
    }
}

/// Encoders in order of preference. Only those whose element exists are
/// probed; the first that works is used. AV1 is hardware only: software AV1
/// is too slow for live streaming, and svtav1enc stalls in its low-delay
/// mode (GStreamer 1.28, SVT-AV1 4.2).
#[cfg(target_os = "linux")]
pub const CANDIDATES: &[Candidate] = &[
    c("nvh264enc", "NVENC", Codec::H264, true, Upload::Cuda),
    c("vah264enc", "VA-API", Codec::H264, true, Upload::Va),
    c(
        "vah264lpenc",
        "VA-API (low power)",
        Codec::H264,
        true,
        Upload::Va,
    ),
    c("x264enc", "x264", Codec::H264, false, Upload::System),
    c(
        "openh264enc",
        "OpenH264",
        Codec::H264,
        false,
        Upload::System,
    ),
    c("nvav1enc", "NVENC", Codec::Av1, true, Upload::Cuda),
    c("vaav1enc", "VA-API", Codec::Av1, true, Upload::Va),
    c(
        "vaav1lpenc",
        "VA-API (low power)",
        Codec::Av1,
        true,
        Upload::Va,
    ),
];

#[cfg(windows)]
pub const CANDIDATES: &[Candidate] = &[
    c("nvd3d11h264enc", "NVENC", Codec::H264, true, Upload::D3d11),
    c("nvh264enc", "NVENC", Codec::H264, true, Upload::D3d11),
    c("amfh264enc", "AMD AMF", Codec::H264, true, Upload::D3d11),
    c(
        "qsvh264enc",
        "Intel Quick Sync",
        Codec::H264,
        true,
        Upload::D3d11,
    ),
    c(
        "mfh264enc",
        "Media Foundation",
        Codec::H264,
        true,
        Upload::D3d11,
    ),
    c("x264enc", "x264", Codec::H264, false, Upload::System),
    c(
        "openh264enc",
        "OpenH264",
        Codec::H264,
        false,
        Upload::System,
    ),
    c("nvd3d11av1enc", "NVENC", Codec::Av1, true, Upload::D3d11),
    c("nvav1enc", "NVENC", Codec::Av1, true, Upload::D3d11),
    c("amfav1enc", "AMD AMF", Codec::Av1, true, Upload::D3d11),
    c(
        "qsvav1enc",
        "Intel Quick Sync",
        Codec::Av1,
        true,
        Upload::D3d11,
    ),
];

#[cfg(not(any(target_os = "linux", windows)))]
pub const CANDIDATES: &[Candidate] = &[
    c("x264enc", "x264", Codec::H264, false, Upload::System),
    c(
        "openh264enc",
        "OpenH264",
        Codec::H264,
        false,
        Upload::System,
    ),
];

/// Encoder settings.
#[derive(Clone, Copy, Debug)]
pub struct Settings {
    pub bitrate_kbps: u32,
    pub fps: u32,
    /// Seconds between keyframes. Viewers who join or lose packets ask for
    /// one at once (PLI), so this can be long, which saves bits on screen
    /// content.
    pub keyframe_secs: u32,
}

impl Candidate {
    pub fn by_factory(name: &str) -> Option<&'static Candidate> {
        CANDIDATES.iter().find(|c| c.factory == name)
    }

    /// Whether the element is installed.
    pub fn installed(&self) -> bool {
        gst::ElementFactory::find(self.factory).is_some()
    }

    /// The converter in front of the encoder, as a launch description,
    /// scaled to `height` (keeping the aspect ratio) when given.
    pub fn upload_description(&self, height: Option<u32>) -> String {
        let size = height.map(|h| format!(",height={h}")).unwrap_or_default();
        match self.upload {
            // A description cannot end with bare caps, hence capsfilter.
            Upload::System => {
                format!("videoconvertscale ! capsfilter caps=\"video/x-raw,format=I420{size}\"")
            }
            Upload::Va => format!(
                "vapostproc ! capsfilter caps=\"video/x-raw(memory:VAMemory),format=NV12{size}\""
            ),
            Upload::Cuda => format!(
                "cudaupload ! cudaconvertscale ! capsfilter caps=\"video/x-raw(memory:CUDAMemory),format=NV12{size}\""
            ),
            Upload::D3d11 => format!(
                "d3d11upload ! d3d11convert ! capsfilter caps=\"video/x-raw(memory:D3D11Memory),format=NV12{size}\""
            ),
        }
    }

    /// The caps the encoder's output is restricted to. H.264 without
    /// B-frames, preferring High, then Main, then Constrained Baseline
    /// (OpenH264 only does the last); MediaMTX accepts all three.
    pub fn output_caps(&self) -> gst::Caps {
        match self.codec {
            Codec::H264 => "video/x-h264,stream-format=byte-stream,alignment=au,profile=(string){ high, main, constrained-baseline }"
                .parse()
                .expect("valid caps"),
            Codec::Av1 => "video/x-av1".parse().expect("valid caps"),
        }
    }

    /// Creates the encoder element, configured for live streaming.
    pub fn make(&self, s: &Settings) -> anyhow::Result<gst::Element> {
        let enc = gst::ElementFactory::make(self.factory)
            .build()
            .with_context(|| format!("creating {}", self.factory))?;
        configure(&enc, s);
        Ok(enc)
    }

    /// Encodes test frames through the same converter until the first
    /// encoded frame comes out, to check the encoder works on this machine.
    pub fn probe(&self) -> anyhow::Result<()> {
        if !self.installed() {
            bail!("{} is not installed", self.factory);
        }
        let desc = format!(
            "videotestsrc is-live=true ! video/x-raw,width=640,height=360,framerate=30/1 ! {} ! {} name=enc ! fakesink name=out",
            self.upload_description(None),
            self.factory
        );
        let pipeline = gst::parse::launch(&desc)?
            .downcast::<gst::Pipeline>()
            .map_err(|_| anyhow!("not a pipeline"))?;
        let enc = pipeline.by_name("enc").expect("named in the description");
        configure(
            &enc,
            &Settings {
                bitrate_kbps: 1000,
                fps: 30,
                keyframe_secs: 1,
            },
        );
        let (tx, rx) = std::sync::mpsc::channel();
        let pad = pipeline
            .by_name("out")
            .and_then(|o| o.static_pad("sink"))
            .expect("fakesink has a sink pad");
        pad.add_probe(gst::PadProbeType::BUFFER, move |_, _| {
            let _ = tx.send(());
            gst::PadProbeReturn::Remove
        });

        pipeline.set_state(gst::State::Playing)?;
        let bus = pipeline.bus().expect("pipelines have a bus");
        let deadline = std::time::Instant::now() + Duration::from_secs(10);
        let result = loop {
            if rx.try_recv().is_ok() {
                break Ok(());
            }
            if let Some(msg) = bus.timed_pop_filtered(
                gst::ClockTime::from_mseconds(50),
                &[gst::MessageType::Error],
            ) && let gst::MessageView::Error(e) = msg.view()
            {
                break Err(anyhow!("{}", e.error()));
            }
            if std::time::Instant::now() > deadline {
                break Err(anyhow!("no output after 10 seconds"));
            }
        };
        let _ = pipeline.set_state(gst::State::Null);
        result
    }
}

/// Picks an encoder: the one named, or the first candidate for the codec
/// that passes the probe. Returns it with the reasons the ones before it
/// were skipped.
pub fn choose(
    codec: Codec,
    name: Option<&str>,
) -> anyhow::Result<(&'static Candidate, Vec<String>)> {
    if let Some(name) = name {
        let cand = Candidate::by_factory(name).ok_or_else(|| anyhow!("unknown encoder {name}"))?;
        cand.probe()
            .with_context(|| format!("{name} does not work on this machine"))?;
        return Ok((cand, vec![]));
    }
    let mut skipped = vec![];
    for cand in CANDIDATES
        .iter()
        .filter(|c| c.codec == codec && c.installed())
    {
        match cand.probe() {
            Ok(()) => return Ok((cand, skipped)),
            Err(e) => skipped.push(format!("{} ({}): {e:#}", cand.label, cand.factory)),
        }
    }
    bail!(
        "no working {codec:?} encoder; tried: {}",
        skipped.join("; ")
    )
}

/// Sets a property from a string if the element has it and accepts the
/// value, so one table of settings works across GStreamer versions and
/// vendors.
fn set(enc: &gst::Element, name: &str, value: &str) {
    let Some(pspec) = enc.find_property(name) else {
        return;
    };
    if !pspec.flags().contains(glib::ParamFlags::WRITABLE) {
        return;
    }
    match glib::Value::deserialize_with_pspec(value, &pspec) {
        Ok(v) => enc.set_property_from_value(name, &v),
        Err(_) => tracing::debug!("{}: {name}={value} is not supported", enc.name()),
    }
}

fn configure(enc: &gst::Element, s: &Settings) {
    let gop = (s.fps * s.keyframe_secs).max(1).to_string();
    let factory = enc
        .factory()
        .map(|f| f.name().to_string())
        .unwrap_or_default();
    match factory.as_str() {
        "x264enc" => {
            set(enc, "tune", "zerolatency");
            set(enc, "speed-preset", "veryfast");
            set(enc, "bframes", "0");
            set(enc, "key-int-max", &gop);
        }
        "openh264enc" => {
            set(enc, "rate-control", "bitrate");
            set(enc, "usage-type", "screen");
            set(enc, "complexity", "low");
            set(enc, "gop-size", &gop);
        }
        f if f.starts_with("va") => {
            set(enc, "rate-control", "cbr");
            set(enc, "b-frames", "0");
            set(enc, "key-int-max", &gop);
            set(enc, "ref-frames", "1");
            // 1 is best quality, 7 fastest.
            set(enc, "target-usage", "6");
        }
        f if f.starts_with("nv") => {
            set(enc, "rc-mode", "cbr");
            set(enc, "bframes", "0");
            set(enc, "b-frames", "0");
            set(enc, "gop-size", &gop);
            set(enc, "zerolatency", "true");
            set(enc, "tune", "ultra-low-latency");
            set(enc, "preset", "p4");
        }
        f if f.starts_with("amf") => {
            set(enc, "rate-control", "cbr");
            set(enc, "usage", "ultra-low-latency");
            set(enc, "gop-size", &gop);
            set(enc, "b-frames", "0");
        }
        f if f.starts_with("qsv") => {
            set(enc, "rate-control", "cbr");
            set(enc, "b-frames", "0");
            set(enc, "gop-size", &gop);
            set(enc, "low-latency", "true");
        }
        f if f.starts_with("mf") => {
            set(enc, "rc-mode", "cbr");
            set(enc, "bframes", "0");
            set(enc, "gop-size", &gop);
            set(enc, "low-latency", "true");
        }
        _ => {}
    }
    set_bitrate(enc, s.bitrate_kbps);
}

/// Changes the bitrate, also while live (the server's target bitrate, or
/// the user's setting).
pub fn set_bitrate(enc: &gst::Element, kbps: u32) {
    let factory = enc
        .factory()
        .map(|f| f.name().to_string())
        .unwrap_or_default();
    match factory.as_str() {
        "openh264enc" => set(enc, "bitrate", &(kbps * 1000).to_string()),
        _ => set(enc, "bitrate", &kbps.to_string()),
    }
}

/// Every candidate for this platform and whether it works here.
pub fn report() -> Vec<(&'static Candidate, Result<(), String>)> {
    CANDIDATES
        .iter()
        .map(|c| {
            (
                c,
                if c.installed() {
                    c.probe().map_err(|e| format!("{e:#}"))
                } else {
                    Err("not installed".into())
                },
            )
        })
        .collect()
}
