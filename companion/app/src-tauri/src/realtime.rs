//! The app's realtime WebSocket. While it is open, the website knows the
//! app is running and can ask it to go live (goLive). While live, the app
//! subscribes to its stream's room to receive the server's target bitrate
//! (bitrateTarget). It reconnects with backoff until stopped.

use futures_util::{SinkExt, StreamExt};
use serde::Deserialize;
use std::time::Duration;
use tokio::sync::mpsc;
use tokio_tungstenite::tungstenite::client::IntoClientRequest;
use tokio_tungstenite::tungstenite::protocol::frame::coding::CloseCode;
use tokio_tungstenite::tungstenite::{Message, http::HeaderValue};

/// What the server tells the app.
#[derive(Debug)]
pub enum Event {
    /// The website asked the app to go live, optionally into a room.
    GoLive { room: Option<String> },
    /// The highest bitrate the stream's viewers can take, in bit/s for the
    /// whole stream; 0 means no limit.
    BitrateTarget { stream_id: String, max_bitrate: u64 },
    /// The app session ended (signed out or revoked on the website).
    SignedOut,
}

/// The server closes the socket with this code when the session ends.
const SIGNED_OUT: u16 = 4001;

#[derive(Deserialize)]
#[serde(tag = "type", rename_all = "camelCase")]
enum ServerMessage {
    GoLive {
        #[serde(default)]
        room: Option<String>,
    },
    #[serde(rename_all = "camelCase")]
    BitrateTarget { stream_id: String, max_bitrate: u64 },
    #[serde(other)]
    Other,
}

/// A running connection; dropping it closes the socket.
pub struct Realtime {
    room: tokio::sync::watch::Sender<Option<String>>,
    task: tokio::task::JoinHandle<()>,
}

impl Drop for Realtime {
    fn drop(&mut self) {
        self.task.abort();
    }
}

impl Realtime {
    pub fn start(server: &url::Url, token: String, events: mpsc::UnboundedSender<Event>) -> Self {
        let mut url = server.join("/api/realtime").expect("valid path");
        let _ = url.set_scheme(if server.scheme() == "https" {
            "wss"
        } else {
            "ws"
        });
        let (room, room_rx) = tokio::sync::watch::channel(None);
        let task = tokio::spawn(run(url, token, events, room_rx));
        Self { room, task }
    }

    /// Follows a room (the live stream's), or none.
    pub fn follow(&self, room: Option<String>) {
        self.room.send_replace(room);
    }
}

async fn run(
    url: url::Url,
    token: String,
    events: mpsc::UnboundedSender<Event>,
    mut room: tokio::sync::watch::Receiver<Option<String>>,
) {
    let mut backoff = Duration::from_secs(1);
    loop {
        match connect(&url, &token, &events, &mut room).await {
            Ok(Closed::SignedOut) => {
                let _ = events.send(Event::SignedOut);
                return;
            }
            Ok(Closed::Other) => backoff = Duration::from_secs(1),
            Err(e) => tracing::debug!("realtime connection failed: {e:#}"),
        }
        tokio::time::sleep(backoff).await;
        backoff = (backoff * 2).min(Duration::from_secs(30));
    }
}

enum Closed {
    SignedOut,
    Other,
}

async fn connect(
    url: &url::Url,
    token: &str,
    events: &mpsc::UnboundedSender<Event>,
    room: &mut tokio::sync::watch::Receiver<Option<String>>,
) -> anyhow::Result<Closed> {
    let mut req = url.as_str().into_client_request()?;
    req.headers_mut().insert(
        "Authorization",
        HeaderValue::from_str(&format!("Bearer {token}"))?,
    );
    let (ws, _) = tokio_tungstenite::connect_async(req).await?;
    tracing::info!("connected to the realtime socket");
    let (mut tx, mut rx) = ws.split();

    let subscribe =
        |r: &str| Message::text(serde_json::json!({ "type": "subscribe", "room": r }).to_string());
    let unsubscribe = |r: &str| {
        Message::text(serde_json::json!({ "type": "unsubscribe", "room": r }).to_string())
    };
    let mut following = room.borrow_and_update().clone();
    if let Some(r) = &following {
        tx.send(subscribe(r)).await?;
    }

    loop {
        tokio::select! {
            changed = room.changed() => {
                if changed.is_err() {
                    return Ok(Closed::Other);
                }
                let next = room.borrow_and_update().clone();
                if next != following {
                    if let Some(r) = &following {
                        tx.send(unsubscribe(r)).await?;
                    }
                    if let Some(r) = &next {
                        tx.send(subscribe(r)).await?;
                    }
                    following = next;
                }
            }
            msg = rx.next() => {
                let Some(msg) = msg else { return Ok(Closed::Other) };
                match msg? {
                    Message::Text(text) => match serde_json::from_str::<ServerMessage>(&text) {
                        Ok(ServerMessage::GoLive { room }) => {
                            let _ = events.send(Event::GoLive { room: room.filter(|r| !r.is_empty()) });
                        }
                        Ok(ServerMessage::BitrateTarget { stream_id, max_bitrate }) => {
                            let _ = events.send(Event::BitrateTarget { stream_id, max_bitrate });
                        }
                        Ok(ServerMessage::Other) | Err(_) => {}
                    },
                    Message::Close(frame) => {
                        let signed_out = frame.is_some_and(|f| f.code == CloseCode::Library(SIGNED_OUT));
                        return Ok(if signed_out { Closed::SignedOut } else { Closed::Other });
                    }
                    _ => {}
                }
            }
        }
    }
}
