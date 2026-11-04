//! What can be shared on Windows, for the app's picker: monitors and
//! top-level windows, with a thumbnail of each taken through the same
//! capture (Windows.Graphics.Capture) the stream uses.

use anyhow::{Context, anyhow};
use gst::prelude::*;
use serde::Serialize;
use std::time::Duration;
use windows_sys::Win32::Foundation::{HWND, LPARAM, RECT, TRUE};
use windows_sys::Win32::Graphics::Dwm::{DWMWA_CLOAKED, DwmGetWindowAttribute};
use windows_sys::Win32::Graphics::Gdi::{
    EnumDisplayMonitors, GetMonitorInfoW, HDC, HMONITOR, MONITORINFO,
};
use windows_sys::Win32::UI::WindowsAndMessaging::{
    EnumWindows, GW_OWNER, GWL_EXSTYLE, GetClassNameW, GetWindow, GetWindowLongW, GetWindowRect,
    GetWindowTextLengthW, GetWindowTextW, GetWindowThreadProcessId, IsIconic, IsWindowVisible,
    MONITORINFOF_PRIMARY, WS_EX_TOOLWINDOW,
};
use windows_sys::core::BOOL;

use super::VideoSource;

/// A monitor or a window that can be shared.
#[derive(Clone, Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Shareable {
    /// "monitor-handle:<HMONITOR>" or "window:<HWND>", for `parse`.
    pub id: String,
    /// "monitor" or "window".
    pub kind: &'static str,
    pub title: String,
    pub width: i32,
    pub height: i32,
    pub primary: bool,
}

/// Turns a picker ID back into a source.
pub fn parse(id: &str) -> Option<VideoSource> {
    if let Some(h) = id.strip_prefix("monitor-handle:") {
        return h.parse().ok().map(VideoSource::MonitorHandle);
    }
    if let Some(h) = id.strip_prefix("window:") {
        return h.parse().ok().map(VideoSource::Window);
    }
    if let Some(n) = id.strip_prefix("monitor:") {
        return n.parse().ok().map(VideoSource::Monitor);
    }
    None
}

/// Monitors first (the primary one first), then windows in Z order.
pub fn list() -> Vec<Shareable> {
    let mut monitors: Vec<Shareable> = Vec::new();
    // SAFETY: the callback only runs during the call and gets a pointer
    // to `monitors`, which outlives it.
    unsafe {
        EnumDisplayMonitors(
            std::ptr::null_mut(),
            std::ptr::null(),
            Some(on_monitor),
            &mut monitors as *mut _ as LPARAM,
        );
    }
    monitors.sort_by_key(|m| !m.primary);
    for (i, m) in monitors.iter_mut().enumerate() {
        m.title = format!("Screen {}{}", i + 1, if m.primary { " (main)" } else { "" });
    }
    let mut windows: Vec<Shareable> = Vec::new();
    // SAFETY: as above.
    unsafe {
        EnumWindows(Some(on_window), &mut windows as *mut _ as LPARAM);
    }
    monitors.into_iter().chain(windows).collect()
}

unsafe extern "system" fn on_monitor(
    monitor: HMONITOR,
    _: HDC,
    _: *mut RECT,
    data: LPARAM,
) -> BOOL {
    // SAFETY: `data` is the Vec that `list` passed.
    let list = unsafe { &mut *(data as *mut Vec<Shareable>) };
    let mut info: MONITORINFO = unsafe { std::mem::zeroed() };
    info.cbSize = std::mem::size_of::<MONITORINFO>() as u32;
    if unsafe { GetMonitorInfoW(monitor, &mut info) } != 0 {
        let r = info.rcMonitor;
        list.push(Shareable {
            id: format!("monitor-handle:{}", monitor as usize),
            kind: "monitor",
            title: String::new(),
            width: r.right - r.left,
            height: r.bottom - r.top,
            primary: info.dwFlags & MONITORINFOF_PRIMARY != 0,
        });
    }
    TRUE
}

/// Window classes of the desktop itself, never worth sharing.
const SHELL_CLASSES: &[&str] = &[
    "Progman",
    "WorkerW",
    "Shell_TrayWnd",
    "Shell_SecondaryTrayWnd",
];

unsafe extern "system" fn on_window(hwnd: HWND, data: LPARAM) -> BOOL {
    // SAFETY: `data` is the Vec that `list` passed; the rest are plain
    // queries on a window handle the system just gave us.
    unsafe {
        let list = &mut *(data as *mut Vec<Shareable>);
        if IsWindowVisible(hwnd) == 0 || IsIconic(hwnd) != 0 || !GetWindow(hwnd, GW_OWNER).is_null()
        {
            return TRUE;
        }
        if GetWindowLongW(hwnd, GWL_EXSTYLE) as u32 & WS_EX_TOOLWINDOW != 0 {
            return TRUE;
        }
        let mut cloaked: u32 = 0;
        let ok = DwmGetWindowAttribute(
            hwnd,
            DWMWA_CLOAKED as u32,
            &mut cloaked as *mut u32 as *mut _,
            4,
        );
        if ok == 0 && cloaked != 0 {
            return TRUE;
        }
        let mut pid = 0u32;
        GetWindowThreadProcessId(hwnd, &mut pid);
        if pid == std::process::id() {
            return TRUE;
        }
        let mut class = [0u16; 64];
        let n = GetClassNameW(hwnd, class.as_mut_ptr(), class.len() as i32);
        if SHELL_CLASSES.contains(&String::from_utf16_lossy(&class[..n.max(0) as usize]).as_str()) {
            return TRUE;
        }
        let len = GetWindowTextLengthW(hwnd);
        if len <= 0 {
            return TRUE;
        }
        let mut title = vec![0u16; len as usize + 1];
        let n = GetWindowTextW(hwnd, title.as_mut_ptr(), title.len() as i32);
        let mut r: RECT = std::mem::zeroed();
        GetWindowRect(hwnd, &mut r);
        if r.right - r.left < 50 || r.bottom - r.top < 50 {
            return TRUE;
        }
        list.push(Shareable {
            id: format!("window:{}", hwnd as usize),
            kind: "window",
            title: String::from_utf16_lossy(&title[..n.max(0) as usize]),
            width: r.right - r.left,
            height: r.bottom - r.top,
            primary: false,
        });
        TRUE
    }
}

/// A PNG thumbnail of a source, `width` pixels wide: one frame captured
/// the way the stream would capture it.
pub fn thumbnail(id: &str, width: u32) -> anyhow::Result<Vec<u8>> {
    let target = match parse(id).ok_or_else(|| anyhow!("unknown source {id}"))? {
        VideoSource::MonitorHandle(h) => format!("monitor-handle={h}"),
        VideoSource::Monitor(i) => format!("monitor-index={i}"),
        VideoSource::Window(h) => format!("window-handle={h}"),
        VideoSource::Test => return Err(anyhow!("no thumbnail for the test source")),
    };
    let desc = format!(
        "d3d11screencapturesrc capture-api=wgc show-cursor=false {target} ! d3d11download ! videoconvertscale \
         ! video/x-raw,format=RGBA,width={width},pixel-aspect-ratio=1/1 ! appsink name=out max-buffers=1 drop=true"
    );
    let pipeline = gst::parse::launch(&desc)?
        .downcast::<gst::Pipeline>()
        .map_err(|_| anyhow!("not a pipeline"))?;
    let sink = pipeline
        .by_name("out")
        .and_then(|e| e.downcast::<gst_app::AppSink>().ok())
        .ok_or_else(|| anyhow!("no appsink"))?;
    pipeline.set_state(gst::State::Playing)?;
    let sample = sink.try_pull_sample(gst::ClockTime::from_mseconds(
        Duration::from_secs(3).as_millis() as u64,
    ));
    let _ = pipeline.set_state(gst::State::Null);
    let sample = sample.ok_or_else(|| anyhow!("no frame from {id}"))?;
    let info = sample
        .caps()
        .and_then(|c| gst_video::VideoInfo::from_caps(c).ok())
        .context("frame without video caps")?;
    let buffer = sample.buffer().context("frame without data")?;
    let map = buffer.map_readable()?;
    // Rows can be padded; copy them tightly for the encoder.
    let (w, h, stride) = (
        info.width() as usize,
        info.height() as usize,
        info.stride()[0] as usize,
    );
    let mut rgba = Vec::with_capacity(w * h * 4);
    for row in 0..h {
        rgba.extend_from_slice(&map[row * stride..row * stride + w * 4]);
    }
    let mut out = Vec::new();
    let mut enc = png::Encoder::new(&mut out, w as u32, h as u32);
    enc.set_color(png::ColorType::Rgba);
    enc.set_depth(png::BitDepth::Eight);
    enc.write_header()?.write_image_data(&rgba)?;
    Ok(out)
}
