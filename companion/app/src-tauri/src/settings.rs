//! The app's own settings, kept as JSON in the app's config directory.
//! Settings that belong to the account (start mode, link-room visibility)
//! live on the server instead.

use cupstream_media::audio::AudioMode;
use cupstream_media::encoder::Codec;
use serde::{Deserialize, Serialize};
use std::path::PathBuf;

pub const DEFAULT_SERVER: &str = "https://cupstream.larp.love";

/// Resolution and frame-rate presets, up to 4K at 60 fps.
#[derive(Clone, Copy, Debug, Serialize, Deserialize, PartialEq, Eq)]
pub enum Preset {
    #[serde(rename = "720p30")]
    P720_30,
    #[serde(rename = "1080p30")]
    P1080_30,
    #[serde(rename = "1080p60")]
    P1080_60,
    #[serde(rename = "1440p60")]
    P1440_60,
    #[serde(rename = "2160p60")]
    P2160_60,
    /// The source's own size at 60 fps.
    #[serde(rename = "source")]
    Source,
}

impl Preset {
    pub fn height(self) -> Option<u32> {
        match self {
            Preset::P720_30 => Some(720),
            Preset::P1080_30 | Preset::P1080_60 => Some(1080),
            Preset::P1440_60 => Some(1440),
            Preset::P2160_60 => Some(2160),
            Preset::Source => None,
        }
    }

    pub fn fps(self) -> u32 {
        match self {
            Preset::P720_30 | Preset::P1080_30 => 30,
            _ => 60,
        }
    }

    /// The automatic bitrate in kbit/s: what screen content needs to look
    /// sharp with H.264; AV1 needs about a third less.
    pub fn auto_bitrate(self, codec: Codec) -> u32 {
        let h264 = match self {
            Preset::P720_30 => 3000,
            Preset::P1080_30 => 4500,
            Preset::P1080_60 | Preset::Source => 6000,
            Preset::P1440_60 => 9000,
            Preset::P2160_60 => 16000,
        };
        match codec {
            Codec::H264 => h264,
            Codec::Av1 => h264 * 2 / 3,
        }
    }
}

#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "camelCase")]
pub enum Audio {
    ExceptDiscord,
    Everything,
    App,
    None,
}

#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct Settings {
    pub server: String,
    pub preset: Preset,
    /// kbit/s; None picks it from the preset.
    pub bitrate_kbps: Option<u32>,
    /// "h264" or "av1".
    pub codec: String,
    /// An encoder element; None picks the best one that works.
    pub encoder: Option<String>,
    pub audio: Audio,
    /// The application for Audio::App.
    pub audio_app: String,
    /// Starts and stops the stream, e.g. "Ctrl+Shift+F9"; empty: none.
    pub hotkey: String,
    /// The screen-sharing portal's token for the last choice (Linux).
    pub restore_token: Option<String>,
    /// What to capture on X11 and Windows: "monitor:<n>", "window:<id>".
    pub source: Option<String>,
}

impl Default for Settings {
    fn default() -> Self {
        Self {
            // CUPSTREAM_SERVER points a first run at another server, e.g.
            // a development one.
            server: std::env::var("CUPSTREAM_SERVER").unwrap_or_else(|_| DEFAULT_SERVER.into()),
            preset: Preset::P1080_60,
            bitrate_kbps: None,
            codec: "h264".into(),
            encoder: None,
            audio: Audio::ExceptDiscord,
            audio_app: String::new(),
            hotkey: "Ctrl+Shift+F9".into(),
            restore_token: None,
            source: None,
        }
    }
}

impl Settings {
    pub fn codec(&self) -> Codec {
        if self.codec == "av1" {
            Codec::Av1
        } else {
            Codec::H264
        }
    }

    pub fn bitrate(&self) -> u32 {
        self.bitrate_kbps
            .unwrap_or_else(|| self.preset.auto_bitrate(self.codec()))
    }

    pub fn audio_mode(&self) -> AudioMode {
        match self.audio {
            Audio::ExceptDiscord => AudioMode::ExceptDiscord,
            Audio::Everything => AudioMode::Everything,
            Audio::App if !self.audio_app.trim().is_empty() => {
                AudioMode::App(self.audio_app.trim().into())
            }
            Audio::App => AudioMode::ExceptDiscord,
            Audio::None => AudioMode::None,
        }
    }

    pub fn load(path: &PathBuf) -> Self {
        std::fs::read_to_string(path)
            .ok()
            .and_then(|s| serde_json::from_str(&s).ok())
            .unwrap_or_default()
    }

    pub fn save(&self, path: &PathBuf) -> anyhow::Result<()> {
        if let Some(dir) = path.parent() {
            std::fs::create_dir_all(dir)?;
        }
        std::fs::write(path, serde_json::to_string_pretty(self)?)?;
        Ok(())
    }
}
