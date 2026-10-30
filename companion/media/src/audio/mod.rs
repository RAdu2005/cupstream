//! Audio capture. The default leaves out Discord, so viewers do not hear
//! the voice call the streamer is in (overview 6.2): a PipeWire virtual
//! source on Linux, WASAPI process loopback on Windows.

#[cfg(target_os = "linux")]
mod linux;
#[cfg(windows)]
mod windows;

use anyhow::Context;

/// What viewers hear.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum AudioMode {
    /// Everything the computer plays, except Discord.
    ExceptDiscord,
    /// Everything the computer plays.
    Everything,
    /// One application, by name.
    App(String),
    /// A test tone (pipeline tests).
    Test,
    None,
}

/// Which application streams are captured.
#[derive(Clone, Debug)]
pub(crate) enum Filter {
    ExceptDiscord,
    Only(String),
}

/// An application playing audio, and whether viewers hear it.
#[derive(Clone, Debug)]
pub struct AudioStream {
    pub name: String,
    pub captured: bool,
}

/// An open audio capture: a source bin with one raw audio pad, warnings
/// for the user (e.g. Discord cannot be left out), and the router that must
/// stay alive while it runs.
pub struct AudioCapture {
    pub bin: gst::Bin,
    pub warnings: Vec<String>,
    #[cfg(target_os = "linux")]
    router: Option<linux::Router>,
}

impl AudioCapture {
    /// Applications playing audio and whether they are captured, where the
    /// platform can tell.
    pub fn streams(&self) -> Vec<AudioStream> {
        #[cfg(target_os = "linux")]
        if let Some(r) = &self.router {
            return r.streams();
        }
        vec![]
    }
}

fn bin_from(desc: &str) -> anyhow::Result<gst::Bin> {
    gst::parse::bin_from_description(desc, true).with_context(|| format!("creating {desc}"))
}

/// Raw audio as the Opus encoder wants it.
const CONVERT: &str =
    "audioconvert ! audioresample ! capsfilter caps=audio/x-raw,rate=48000,channels=2";

pub fn open(mode: &AudioMode) -> anyhow::Result<Option<AudioCapture>> {
    let simple = |desc: &str, warnings: Vec<String>| -> anyhow::Result<Option<AudioCapture>> {
        Ok(Some(AudioCapture {
            bin: bin_from(&format!("{desc} ! {CONVERT}"))?,
            warnings,
            #[cfg(target_os = "linux")]
            router: None,
        }))
    };
    match mode {
        AudioMode::None => Ok(None),
        AudioMode::Test => simple("audiotestsrc is-live=true wave=ticks", vec![]),
        #[cfg(target_os = "linux")]
        AudioMode::Everything => simple(
            "pipewiresrc client-name=CupStream do-timestamp=true stream-properties=\"props,stream.capture.sink=(string)true\"",
            vec![],
        )
        .or_else(|_| simple("pulsesrc device=@DEFAULT_MONITOR@ client-name=CupStream", vec![])),
        #[cfg(target_os = "linux")]
        AudioMode::ExceptDiscord | AudioMode::App(_) => {
            let filter = match mode {
                AudioMode::App(name) => Filter::Only(name.clone()),
                _ => Filter::ExceptDiscord,
            };
            match linux::Router::start(filter) {
                Ok(router) => Ok(Some(AudioCapture {
                    bin: bin_from(&format!(
                        "pipewiresrc client-name=CupStream do-timestamp=true target-object={} ! {CONVERT}",
                        router.node_name
                    ))?,
                    warnings: vec![],
                    router: Some(router),
                })),
                Err(e) => {
                    tracing::warn!("PipeWire is not available: {e:#}");
                    simple(
                        "pulsesrc device=@DEFAULT_MONITOR@ client-name=CupStream",
                        vec!["This system has no PipeWire, so Discord cannot be left out: viewers hear all system audio.".into()],
                    )
                }
            }
        }
        #[cfg(windows)]
        AudioMode::Everything | AudioMode::ExceptDiscord | AudioMode::App(_) => {
            let (desc, warnings) = windows::source(mode)?;
            simple(&desc, warnings)
        }
        #[cfg(not(any(target_os = "linux", windows)))]
        _ => anyhow::bail!("audio capture is not supported on this system"),
    }
}
