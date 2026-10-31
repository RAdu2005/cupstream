// No console window on Windows in release builds.
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

fn main() {
    cupstream_media::use_bundled_gstreamer();
    cupstream_app::run();
}
