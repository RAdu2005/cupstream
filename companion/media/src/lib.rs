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

/// Uses the GStreamer runtime installed with the app, when there is one: a
/// `gstreamer-1.0` directory of plugins next to the executable, whose DLLs
/// sit next to the executable too (the Windows installer's layout). Only
/// those plugins are loaded, with their own registry file, so a GStreamer
/// installed system-wide cannot interfere; a full installation also hangs
/// while scanning its plugins. Without the directory nothing changes, and
/// the system's GStreamer is used (Linux, development builds).
///
/// Call it first thing in `main`, before any other thread starts: it sets
/// environment variables.
pub fn use_bundled_gstreamer() -> bool {
    let Some(dir) = std::env::current_exe()
        .ok()
        .and_then(|e| e.parent().map(|d| d.join("gstreamer-1.0")))
    else {
        return false;
    };
    if !dir.is_dir() {
        return false;
    }
    let data = std::env::var_os("LOCALAPPDATA")
        .or_else(|| std::env::var_os("XDG_CACHE_HOME"))
        .map(std::path::PathBuf::from)
        .unwrap_or_else(std::env::temp_dir)
        .join("CupStream");
    let _ = std::fs::create_dir_all(&data);
    let registry = data.join(format!(
        "gstreamer-registry-{}.bin",
        env!("CARGO_PKG_VERSION")
    ));
    // SAFETY: called before other threads exist (see above).
    unsafe {
        for var in ["GST_PLUGIN_SYSTEM_PATH_1_0", "GST_PLUGIN_SYSTEM_PATH"] {
            std::env::set_var(var, &dir);
        }
        for var in ["GST_PLUGIN_PATH_1_0", "GST_PLUGIN_PATH"] {
            std::env::set_var(var, "");
        }
        std::env::set_var("GST_REGISTRY_1_0", &registry);
        // The bundle has no gst-plugin-scanner; its plugins are scanned in
        // this process.
        std::env::set_var("GST_REGISTRY_FORK", "no");
    }
    true
}

/// The GStreamer elements the app's pipelines use on this platform, besides
/// the encoders (which are optional, see the encoder module). The Windows
/// bundle's plugin list (companion/scripts/gstreamer-plugins.txt) must cover
/// them; `cupstream-cli check` verifies it.
pub const REQUIRED_ELEMENTS: &[&str] = &[
    // Pipelines.
    "queue",
    "capsfilter",
    "tee",
    "videoconvertscale",
    "videorate",
    "h264parse",
    "audioconvert",
    "audioresample",
    "opusenc",
    "videotestsrc",
    "audiotestsrc",
    // whipclientsink (built in) and the WebRTC stack under it.
    "whipclientsink",
    "webrtcbin",
    "rtpbin",
    "rtph264pay",
    "rtpopuspay",
    "nicesrc",
    "nicesink",
    "dtlssrtpenc",
    "dtlssrtpdec",
    "srtpenc",
    "srtpdec",
    "errorignore",
    "appsrc",
    "appsink",
    "fakesink",
    #[cfg(windows)]
    "d3d11screencapturesrc",
    #[cfg(windows)]
    "d3d11convert",
    #[cfg(windows)]
    "d3d11upload",
    #[cfg(windows)]
    "d3d11download",
    #[cfg(windows)]
    "wasapi2src",
    #[cfg(target_os = "linux")]
    "pipewiresrc",
    #[cfg(target_os = "linux")]
    "ximagesrc",
];

/// The required elements that are not available.
pub fn missing_elements() -> Vec<&'static str> {
    REQUIRED_ELEMENTS
        .iter()
        .copied()
        .filter(|e| gst::ElementFactory::find(e).is_none())
        .collect()
}

/// Initializes GStreamer and registers the built-in whipclientsink.
pub fn init() -> anyhow::Result<()> {
    gst::init()?;
    gstrswebrtc::plugin_register_static()?;
    Ok(())
}
