//! The CupStream API as the desktop app uses it: signing in through the
//! system browser (see the server's package auth), and starting and ending
//! streams.

mod signin;

pub use signin::{SignIn, SignedIn};

use anyhow::{Context, bail};
use serde::{Deserialize, Serialize};
use url::Url;

/// A user as the API describes them.
#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct User {
    pub id: String,
    pub handle: String,
    pub display_name: String,
}

/// An error answer from the API, with its machine-readable code
/// ("already_live", "discord_unavailable", "sign_in_required", …).
#[derive(Debug, Deserialize)]
pub struct ApiError {
    #[serde(skip)]
    pub status: u16,
    pub error: String,
    #[serde(default)]
    pub code: String,
}

impl std::fmt::Display for ApiError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{} ({})", self.error, self.status)
    }
}

impl std::error::Error for ApiError {}

/// The user's settings as the app shows them.
#[derive(Clone, Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Account {
    pub start_mode: String,
    pub obs_fallback: String,
    pub room_slug: String,
    pub visibility: String,
}

/// Where and how to publish: a WHIP endpoint and its Bearer token.
#[derive(Clone, Debug)]
pub struct Destination {
    pub whip_url: String,
    pub token: String,
    /// The stream the server created, which the app ends when it stops
    /// (None for OBS-style stream keys: the stream ends with the WHIP
    /// session).
    pub stream_id: Option<String>,
    pub room_slug: Option<String>,
}

/// Options of POST /api/streams.
#[derive(Clone, Debug, Default)]
pub struct StartOptions {
    /// A room slug; None follows the user's start mode.
    pub room: Option<String>,
    /// Go to the link room when the start mode is Discord (auto) and no
    /// Discord room is possible.
    pub link_fallback: bool,
    /// End the user's stream elsewhere first.
    pub replace: bool,
}

/// The API, signed in with an app session or anonymously.
#[derive(Clone)]
pub struct Client {
    server: Url,
    http: reqwest::Client,
    token: Option<String>,
}

/// The HTTP client every request uses: rustls with the system's roots.
pub(crate) fn http_client() -> anyhow::Result<reqwest::Client> {
    let _ = rustls::crypto::ring::default_provider().install_default();
    Ok(reqwest::Client::builder()
        .user_agent(concat!("CupStream/", env!("CARGO_PKG_VERSION")))
        .tls_backend_rustls()
        .build()?)
}

impl Client {
    /// A client for the server at `server` (e.g. https://cupstream.larp.love),
    /// signed in with an app session token if given.
    pub fn new(server: &str, token: Option<String>) -> anyhow::Result<Self> {
        let server = Url::parse(server).context("invalid server URL")?;
        Ok(Self {
            server,
            http: http_client()?,
            token,
        })
    }

    pub fn server(&self) -> &Url {
        &self.server
    }

    fn url(&self, path: &str) -> Url {
        self.server.join(path).expect("API paths are valid")
    }

    async fn send<T: for<'de> Deserialize<'de>>(
        &self,
        req: reqwest::RequestBuilder,
    ) -> anyhow::Result<T> {
        let req = match &self.token {
            Some(t) => req.bearer_auth(t),
            None => req,
        };
        let resp = req.send().await?;
        let status = resp.status();
        if !status.is_success() {
            let mut err = resp.json::<ApiError>().await.unwrap_or_else(|_| ApiError {
                status: 0,
                error: status
                    .canonical_reason()
                    .unwrap_or("request failed")
                    .to_owned(),
                code: String::new(),
            });
            err.status = status.as_u16();
            return Err(err.into());
        }
        if status == reqwest::StatusCode::NO_CONTENT {
            return Ok(serde_json::from_value(serde_json::Value::Null)?);
        }
        Ok(resp.json().await?)
    }

    /// The signed-in user.
    pub async fn me(&self) -> anyhow::Result<User> {
        #[derive(Deserialize)]
        struct Me {
            user: User,
        }
        let me: Me = self.send(self.http.get(self.url("/api/me"))).await?;
        Ok(me.user)
    }

    /// Ends the app session on the server.
    pub async fn sign_out(&self) -> anyhow::Result<()> {
        self.send::<()>(self.http.post(self.url("/api/auth/logout")))
            .await
    }

    /// Creates a stream for the signed-in user and returns where to publish
    /// it.
    pub async fn start_stream(&self, opts: &StartOptions) -> anyhow::Result<Destination> {
        #[derive(Serialize)]
        #[serde(rename_all = "camelCase")]
        struct Req<'a> {
            source: &'a str,
            #[serde(skip_serializing_if = "Option::is_none")]
            room: Option<&'a str>,
            link_fallback: bool,
            replace: bool,
        }
        #[derive(Deserialize)]
        #[serde(rename_all = "camelCase")]
        struct Resp {
            stream: StreamRef,
            room_slug: String,
            whip_url: String,
            token: String,
        }
        #[derive(Deserialize)]
        struct StreamRef {
            id: String,
        }
        if self.token.is_none() {
            bail!("sign in first");
        }
        let body = Req {
            source: "app",
            room: opts.room.as_deref(),
            link_fallback: opts.link_fallback,
            replace: opts.replace,
        };
        let r: Resp = self
            .send(self.http.post(self.url("/api/streams")).json(&body))
            .await?;
        Ok(Destination {
            whip_url: r.whip_url,
            token: r.token,
            stream_id: Some(r.stream.id),
            room_slug: Some(r.room_slug),
        })
    }

    /// Ends a stream this app started.
    pub async fn end_stream(&self, stream_id: &str) -> anyhow::Result<()> {
        self.send::<()>(
            self.http
                .delete(self.url(&format!("/api/streams/{stream_id}"))),
        )
        .await
    }

    /// Where OBS-style publishing with a stream key goes: /whip, with
    /// ?room= for someone else's room.
    pub fn stream_key_destination(&self, key: &str, room: Option<&str>) -> Destination {
        let mut url = self.url("/whip");
        if let Some(room) = room {
            url.query_pairs_mut().append_pair("room", room);
        }
        Destination {
            whip_url: url.into(),
            token: key.to_owned(),
            stream_id: None,
            room_slug: room.map(Into::into),
        }
    }

    /// The user's server-side settings that the app shows: where new
    /// streams go, and their link room and its visibility.
    pub async fn account(&self) -> anyhow::Result<Account> {
        #[derive(Deserialize)]
        #[serde(rename_all = "camelCase")]
        struct Resp {
            settings: Settings,
            room_slug: String,
        }
        #[derive(Deserialize)]
        #[serde(rename_all = "camelCase")]
        struct Settings {
            start_mode: String,
            obs_fallback: String,
        }
        #[derive(Deserialize)]
        struct Room {
            visibility: Option<String>,
        }
        let a: Resp = self.send(self.http.get(self.url("/api/account"))).await?;
        let room: Room = self
            .send(
                self.http
                    .get(self.url(&format!("/api/rooms/{}", a.room_slug))),
            )
            .await?;
        Ok(Account {
            start_mode: a.settings.start_mode,
            obs_fallback: a.settings.obs_fallback,
            room_slug: a.room_slug,
            visibility: room.visibility.unwrap_or_default(),
        })
    }

    /// Sets where new streams go ("discord_auto" or "link"), keeping the
    /// OBS fallback the server requires alongside it.
    pub async fn set_start_mode(&self, account: &Account, mode: &str) -> anyhow::Result<()> {
        self.send::<serde_json::Value>(
            self.http.patch(self.url("/api/account/settings")).json(
                &serde_json::json!({ "startMode": mode, "obsFallback": account.obs_fallback }),
            ),
        )
        .await
        .map(|_| ())
    }

    /// Sets who may watch the user's link room: "everyone", "signed_in" or
    /// "invite_only".
    pub async fn set_visibility(&self, room_slug: &str, visibility: &str) -> anyhow::Result<()> {
        self.send::<serde_json::Value>(
            self.http
                .patch(self.url(&format!("/api/rooms/{room_slug}")))
                .json(&serde_json::json!({ "visibility": visibility })),
        )
        .await
        .map(|_| ())
    }

    /// The IDs of a room's live streams, oldest first.
    pub async fn room_streams(&self, slug: &str) -> anyhow::Result<Vec<String>> {
        #[derive(Deserialize)]
        struct Room {
            streams: Vec<StreamRef>,
        }
        #[derive(Deserialize)]
        struct StreamRef {
            id: String,
        }
        let room: Room = self
            .send(self.http.get(self.url(&format!("/api/rooms/{slug}"))))
            .await?;
        Ok(room.streams.into_iter().map(|s| s.id).collect())
    }

    /// The WHEP endpoint to watch a stream.
    pub fn whep_url(&self, stream_id: &str) -> Url {
        self.url(&format!("/api/streams/{stream_id}/whep"))
    }

    /// The app session token, if signed in.
    pub fn token(&self) -> Option<&str> {
        self.token.as_deref()
    }

    /// The link a viewer opens to watch a room.
    pub fn room_url(&self, slug: &str) -> Url {
        self.url(&format!("/r/{slug}"))
    }
}
