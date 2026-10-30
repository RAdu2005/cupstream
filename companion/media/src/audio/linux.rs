//! Audio without Discord on Linux, the way venmic does it: a virtual
//! PipeWire source that the app records from, and a client that watches the
//! graph and links every application's output into it, except Discord's.
//! Nothing is rerouted: applications keep playing to the speakers as before;
//! the links only add a copy.

use anyhow::{Context, anyhow};
use pipewire as pw;
use pw::types::ObjectType;
use std::cell::RefCell;
use std::collections::{HashMap, HashSet};
use std::rc::Rc;
use std::sync::{Arc, Mutex, mpsc};
use std::thread::JoinHandle;
use std::time::Duration;

use super::{AudioStream, Filter};

/// Desktop clients whose audio is the voice call. Matched against the
/// stream's application properties, and the
/// names of its process and the process's ancestors (the call runs in a
/// child process). Discord's voice engine plays as "WEBRTC VoiceEngine";
/// only its process properties say that it is Discord.
const DISCORD_NAMES: &[&str] = &[
    "discord", "vesktop", "legcord", "armcord", "webcord", "equibop", "dorion",
];

/// Application properties that name the stream's program.
const NAME_KEYS: &[&str] = &[
    "application.name",
    "application.process.binary",
    "application.id",
    "application.icon-name",
    "pipewire.access.portal.app_id",
    "node.name",
];

/// A running router; dropping it removes the virtual source and its links.
pub struct Router {
    quit: pw::channel::Sender<()>,
    thread: Option<JoinHandle<()>>,
    /// node.name of the virtual source.
    pub node_name: String,
    streams: Arc<Mutex<Vec<AudioStream>>>,
}

impl Router {
    /// Connects to PipeWire, creates the virtual source and starts linking.
    /// Fails when PipeWire is not running (PulseAudio-only systems).
    pub fn start(filter: Filter) -> anyhow::Result<Self> {
        let node_name = format!("cupstream-audio-{}", std::process::id());
        let streams = Arc::new(Mutex::new(Vec::new()));
        let (ready_tx, ready_rx) = mpsc::channel();
        let (quit, quit_rx) = pw::channel::channel();
        let name = node_name.clone();
        let shared = streams.clone();
        let thread = std::thread::Builder::new()
            .name("pipewire-audio".into())
            .spawn(move || {
                if let Err(e) = run(&name, filter, shared, quit_rx, &ready_tx) {
                    let _ = ready_tx.send(Err(e));
                }
            })?;
        match ready_rx.recv_timeout(Duration::from_secs(5)) {
            Ok(Ok(())) => Ok(Self {
                quit,
                thread: Some(thread),
                node_name,
                streams,
            }),
            Ok(Err(e)) => Err(e),
            Err(_) => Err(anyhow!(
                "PipeWire did not create the virtual source in time"
            )),
        }
    }

    /// Applications playing audio, and whether they are captured.
    pub fn streams(&self) -> Vec<AudioStream> {
        self.streams.lock().unwrap().clone()
    }
}

impl Drop for Router {
    fn drop(&mut self) {
        let _ = self.quit.send(());
        if let Some(t) = self.thread.take() {
            let _ = t.join();
        }
    }
}

#[derive(Default)]
struct State {
    sink_id: Option<u32>,
    /// Input ports of the virtual source, by channel (FL, FR).
    sink_ports: HashMap<String, u32>,
    /// Application output streams: node ID to (name, captured). A stream
    /// is not captured (None) until its full properties say who plays it.
    nodes: HashMap<u32, (String, Option<bool>)>,
    /// The streams' node proxies, bound to receive their full properties
    /// (the registry only announces a few).
    bound: HashMap<u32, (pw::node::Node, pw::node::NodeListener)>,
    /// Output ports of application streams: port ID to (node, channel).
    ports: HashMap<u32, (u32, String)>,
    /// Links we created, by (output port, input port).
    links: HashMap<(u32, u32), pw::link::Link>,
}

fn run(
    node_name: &str,
    filter: Filter,
    shared: Arc<Mutex<Vec<AudioStream>>>,
    quit_rx: pw::channel::Receiver<()>,
    ready: &mpsc::Sender<anyhow::Result<()>>,
) -> anyhow::Result<()> {
    pw::init();
    let mainloop = pw::main_loop::MainLoopRc::new(None).context("PipeWire main loop")?;
    let context = pw::context::ContextRc::new(&mainloop, None).context("PipeWire context")?;
    let core = context
        .connect_rc(None)
        .context("PipeWire is not running")?;
    let registry = core.get_registry_rc()?;

    let weak = mainloop.downgrade();
    let _quit = quit_rx.attach(mainloop.loop_(), move |()| {
        if let Some(l) = weak.upgrade() {
            l.quit();
        }
    });

    // A null sink presented as a virtual microphone: its input ports take
    // the applications' audio, the app records its output. Its priority
    // keeps it from becoming anyone's default microphone.
    let sink = core.create_object::<pw::node::Node>(
        "adapter",
        &pw::properties::properties! {
            "factory.name" => "support.null-audio-sink",
            "node.name" => node_name,
            "node.description" => "CupStream (everything except Discord)",
            "media.class" => "Audio/Source/Virtual",
            "audio.position" => "FL,FR",
            "node.virtual" => "true",
            "priority.session" => "0",
            "priority.driver" => "0",
            "object.linger" => "false",
        },
    )?;

    let state = Rc::new(RefCell::new(State::default()));
    let ready = ready.clone();
    let announced = Rc::new(RefCell::new(false));

    let _listener = registry
        .add_listener_local()
        .global({
            let state = state.clone();
            let core = core.clone();
            let shared = shared.clone();
            let name = node_name.to_owned();
            let filter = filter.clone();
            let registry = registry.clone();
            move |g| {
                let Some(props) = g.props else { return };
                let mut st = state.borrow_mut();
                match g.type_ {
                    ObjectType::Node => {
                        if props.get("node.name") == Some(name.as_str()) {
                            st.sink_id = Some(g.id);
                        } else if props.get("media.class") == Some("Stream/Output/Audio") {
                            let label = props
                                .get("application.name")
                                .or(props.get("node.name"))
                                .unwrap_or("unknown")
                                .to_owned();
                            st.nodes.insert(g.id, (label, None));
                            match registry.bind::<pw::node::Node, _>(g) {
                                Ok(node) => {
                                    let listener = node
                                        .add_listener_local()
                                        .info(on_node_info(
                                            g.id,
                                            Rc::downgrade(&state),
                                            core.clone(),
                                            shared.clone(),
                                            filter.clone(),
                                        ))
                                        .register();
                                    st.bound.insert(g.id, (node, listener));
                                }
                                Err(e) => tracing::warn!("reading audio stream {}: {e}", g.id),
                            }
                        }
                    }
                    ObjectType::Port => {
                        let node = props.get("node.id").and_then(|n| n.parse::<u32>().ok());
                        let channel = props.get("audio.channel").unwrap_or("MONO").to_owned();
                        let out = props.get("port.direction") == Some("out");
                        let monitor = props.get("port.monitor") == Some("true");
                        match node {
                            Some(n) if Some(n) == st.sink_id && !out => {
                                st.sink_ports.insert(channel, g.id);
                            }
                            Some(n) if out && !monitor => {
                                st.ports.insert(g.id, (n, channel));
                            }
                            _ => {}
                        }
                    }
                    _ => return,
                }
                reconcile(&mut st, &core);
                publish(&st, &shared);
            }
        })
        .global_remove({
            let state = state.clone();
            let shared = shared.clone();
            move |id| {
                let mut st = state.borrow_mut();
                st.nodes.remove(&id);
                st.bound.remove(&id);
                st.ports.remove(&id);
                st.links.retain(|(o, i), _| *o != id && *i != id);
                publish(&st, &shared);
            }
        })
        .register();

    // Report ready once the virtual source and both its ports exist.
    let _core_listener = core
        .add_listener_local()
        .done({
            let state = state.clone();
            let announced = announced.clone();
            let ready = ready.clone();
            move |_, _| {
                let st = state.borrow();
                if !*announced.borrow() && st.sink_ports.len() >= 2 {
                    *announced.borrow_mut() = true;
                    let _ = ready.send(Ok(()));
                }
            }
        })
        .register();
    // Each sync answers once everything before it was processed; repeat
    // until the ports appeared.
    let timer = mainloop.loop_().add_timer({
        let core = core.clone();
        let announced = announced.clone();
        move |_| {
            if !*announced.borrow() {
                let _ = core.sync(0);
            }
        }
    });
    let _ = timer.update_timer(
        Some(Duration::from_millis(1)),
        Some(Duration::from_millis(50)),
    );

    mainloop.run();
    let mut st = state.borrow_mut();
    st.links.clear();
    st.bound.clear();
    drop(st);
    drop(sink);
    Ok(())
}

/// Decides whether a stream is captured once its full properties arrive,
/// and again when they change.
fn on_node_info(
    id: u32,
    state: std::rc::Weak<RefCell<State>>,
    core: pw::core::CoreRc,
    shared: Arc<Mutex<Vec<AudioStream>>>,
    filter: Filter,
) -> impl Fn(&pw::node::NodeInfoRef) + 'static {
    let own_pid = std::process::id();
    move |info| {
        // Later events report state changes without the properties.
        if !info.change_mask().contains(pw::node::NodeChangeMask::PROPS) {
            return;
        }
        let (Some(state), Some(props)) = (state.upgrade(), info.props()) else {
            return;
        };
        let mut st = state.borrow_mut();
        let pid = props
            .get("application.process.id")
            .and_then(|p| p.parse::<u32>().ok());
        let names = program_names(|k| props.get(k), pid);
        let captured = pid != Some(own_pid) && included(&filter, &names);
        let Some(entry) = st.nodes.get_mut(&id) else {
            return;
        };
        if entry.1 != Some(captured) {
            tracing::info!(
                "audio stream {:?} ({}) captured: {captured}",
                entry.0,
                names.join(", ")
            );
            entry.1 = Some(captured);
        }
        reconcile(&mut st, &core);
        publish(&st, &shared);
    }
}

/// Creates the links that should exist and drops those that should not.
fn reconcile(st: &mut State, core: &pw::core::CoreRc) {
    let (Some(sink), Some(left), Some(right)) =
        (st.sink_id, st.sink_ports.get("FL"), st.sink_ports.get("FR"))
    else {
        return;
    };
    let mut want = HashSet::new();
    for (&port, (node, channel)) in &st.ports {
        if !st
            .nodes
            .get(node)
            .is_some_and(|(_, captured)| *captured == Some(true))
        {
            continue;
        }
        let ch = channel.as_str();
        let to_left = matches!(ch, "FL" | "RL" | "SL" | "FLC" | "FC" | "LFE" | "MONO");
        let to_right = matches!(ch, "FR" | "RR" | "SR" | "FRC" | "FC" | "LFE" | "MONO");
        if to_left {
            want.insert((port, *node, *left));
        }
        if to_right {
            want.insert((port, *node, *right));
        }
    }
    st.links
        .retain(|key, _| want.iter().any(|(o, _, i)| (*o, *i) == *key));
    for (out, node, input) in want {
        if st.links.contains_key(&(out, input)) {
            continue;
        }
        let link = core.create_object::<pw::link::Link>(
            "link-factory",
            &pw::properties::properties! {
                "link.output.node" => node.to_string(),
                "link.output.port" => out.to_string(),
                "link.input.node" => sink.to_string(),
                "link.input.port" => input.to_string(),
                "object.linger" => "false",
            },
        );
        match link {
            Ok(l) => {
                st.links.insert((out, input), l);
            }
            Err(e) => tracing::warn!("linking audio port {out} failed: {e}"),
        }
    }
}

fn publish(st: &State, shared: &Arc<Mutex<Vec<AudioStream>>>) {
    let mut list: Vec<AudioStream> = st
        .nodes
        .values()
        .map(|(name, captured)| AudioStream {
            name: name.clone(),
            captured: *captured == Some(true),
        })
        .collect();
    list.sort_by(|a, b| a.name.cmp(&b.name));
    *shared.lock().unwrap() = list;
}

/// The names a stream's program goes by: its application properties, and
/// the executable of its process and of the ancestors that run the same
/// executable (Discord plays its voice from a child process). The walk stops
/// at another program, so a browser that Discord happened to start is not
/// taken for Discord.
fn program_names<'a>(get: impl Fn(&str) -> Option<&'a str>, pid: Option<u32>) -> Vec<String> {
    let mut names: Vec<String> = NAME_KEYS
        .iter()
        .filter_map(|k| get(k))
        .map(str::to_lowercase)
        .collect();
    let exe = |p: u32| std::fs::read_link(format!("/proc/{p}/exe")).ok();
    let Some(first) = pid.filter(|p| *p > 1) else {
        return names;
    };
    let program = exe(first);
    let mut pid = Some(first);
    for _ in 0..16 {
        let Some(p) = pid.filter(|p| *p > 1) else {
            break;
        };
        if p != first && (program.is_none() || exe(p) != program) {
            break;
        }
        if let Some(e) = exe(p) {
            names.push(e.to_string_lossy().to_lowercase());
        }
        if let Ok(comm) = std::fs::read_to_string(format!("/proc/{p}/comm")) {
            names.push(comm.trim().to_lowercase());
        }
        pid = parent_pid(p);
    }
    names
}

fn parent_pid(pid: u32) -> Option<u32> {
    let stat = std::fs::read_to_string(format!("/proc/{pid}/stat")).ok()?;
    // The command name is in parentheses and may contain spaces.
    let rest = &stat[stat.rfind(')')? + 2..];
    rest.split(' ').nth(1)?.parse().ok()
}

/// Whether a stream whose program goes by `names` is captured.
pub(super) fn included(filter: &Filter, names: &[String]) -> bool {
    match filter {
        Filter::ExceptDiscord => !names
            .iter()
            .any(|n| DISCORD_NAMES.iter().any(|d| n.contains(d))),
        Filter::Only(app) => {
            let app = app.to_lowercase();
            names.iter().any(|n| n.contains(&app))
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn names(v: &[&str]) -> Vec<String> {
        v.iter().map(|s| s.to_string()).collect()
    }

    #[test]
    fn discord_is_left_out() {
        let f = Filter::ExceptDiscord;
        assert!(!included(
            &f,
            &names(&[
                "webrtc voiceengine",
                "/home/u/.config/discord/app-1.0.167/discord"
            ])
        ));
        assert!(!included(
            &f,
            &names(&["chromium", "/usr/lib/electron/electron", "vesktop"])
        ));
        assert!(!included(&f, &names(&["com.discordapp.discord"])));
        assert!(included(
            &f,
            &names(&["firefox", "/usr/lib/firefox/firefox"])
        ));
        assert!(included(&f, &names(&["spotify"])));
    }

    #[test]
    fn one_application() {
        let f = Filter::Only("Spotify".into());
        assert!(included(&f, &names(&["spotify"])));
        assert!(!included(&f, &names(&["firefox"])));
    }

    #[test]
    fn parent_of_this_process() {
        let me = std::process::id();
        assert!(parent_pid(me).is_some_and(|p| p != me));
        let names = program_names(|_| None, Some(me));
        assert!(!names.is_empty());
    }
}
