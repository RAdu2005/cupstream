//! Audio without Discord on Windows: WASAPI process loopback
//! (wasapi2src), which records everything except one process tree. It needs
//! Windows build 20348 or later (in practice Windows 11); older versions
//! record all system audio, with a warning.

use super::AudioMode;
use sysinfo::{Pid, ProcessesToUpdate, System};

/// Executables of Discord and Discord clients; their root process is
/// excluded with everything it starts.
const DISCORD_EXES: &[&str] = &[
    "discord.exe",
    "discordptb.exe",
    "discordcanary.exe",
    "vesktop.exe",
    "legcord.exe",
    "equibop.exe",
];

/// The first build with process loopback.
const PROCESS_LOOPBACK_BUILD: u32 = 20348;

fn windows_build() -> Option<u32> {
    System::kernel_version()?
        .split('.')
        .next_back()?
        .parse()
        .ok()
}

/// Root processes (whose parent has another name) of programs matching
/// `matches`.
fn root_processes(matches: impl Fn(&str) -> bool) -> Vec<(u32, String)> {
    let Ok(mut sys) = System::new() else {
        return vec![];
    };
    sys.refresh_processes(ProcessesToUpdate::All, true);
    let name_of = |pid: Pid| {
        sys.process(pid)
            .map(|p| p.name().to_string_lossy().to_lowercase())
    };
    let mut roots: Vec<(u32, String)> = sys
        .processes()
        .iter()
        .filter_map(|(pid, p)| {
            let name = p.name().to_string_lossy().to_lowercase();
            if !matches(&name) {
                return None;
            }
            let parent_same = p.parent().and_then(name_of).is_some_and(|n| n == name);
            (!parent_same).then(|| (pid.as_u32(), name))
        })
        .collect();
    roots.sort();
    roots
}

/// The wasapi2src description for a mode, and warnings for the user.
pub fn source(mode: &AudioMode) -> anyhow::Result<(String, Vec<String>)> {
    let all = "wasapi2src loopback=true low-latency=true".to_owned();
    let build = windows_build().unwrap_or(0);
    if *mode == AudioMode::Everything {
        return Ok((all, vec![]));
    }
    if build < PROCESS_LOOPBACK_BUILD {
        return Ok((
            all,
            vec![format!(
                "This version of Windows (build {build}) cannot leave out one application: viewers hear all system audio, including Discord. Windows 11 can."
            )],
        ));
    }
    match mode {
        AudioMode::App(app) => {
            let app = app.to_lowercase();
            let Some((pid, _)) = root_processes(|n| n.contains(&app)).into_iter().next() else {
                anyhow::bail!("{app} is not running");
            };
            Ok((
                format!(
                    "wasapi2src loopback=true low-latency=true loopback-mode=include-process-tree loopback-target-pid={pid}"
                ),
                vec![],
            ))
        }
        _ => {
            let roots = root_processes(|n| DISCORD_EXES.contains(&n));
            let mut warnings = vec![];
            let Some((pid, name)) = roots.first() else {
                warnings.push("Discord is not running. If you start it while live, viewers will hear it until you go live again.".into());
                return Ok((all, warnings));
            };
            if roots.len() > 1 {
                warnings.push(format!(
                    "Only one Discord app can be left out; viewers will hear the others ({}).",
                    roots[1..]
                        .iter()
                        .map(|(_, n)| n.as_str())
                        .collect::<Vec<_>>()
                        .join(", ")
                ));
            }
            tracing::info!("leaving out {name} (process {pid}) and its children");
            Ok((
                format!(
                    "wasapi2src loopback=true low-latency=true loopback-mode=exclude-process-tree loopback-target-pid={pid}"
                ),
                warnings,
            ))
        }
    }
}
