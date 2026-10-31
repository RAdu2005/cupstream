//! CupStream's media pipeline for the desktop app: screen capture,
//! hardware encoding with fallbacks, audio without Discord, and publishing
//! over WHIP with whipclientsink, which is built in rather than loaded from
//! the system's GStreamer plugins.

pub mod audio;
pub mod capture;
pub mod encoder;
pub mod publish;
pub mod watch;

pub use gst;

/// Initializes GStreamer and registers the built-in whipclientsink.
pub fn init() -> anyhow::Result<()> {
    gst::init()?;
    gstrswebrtc::plugin_register_static()?;
    Ok(())
}
