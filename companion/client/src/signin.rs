//! Signing in through the system browser (OAuth for native apps, RFC 8252,
//! with PKCE): the app listens on a loopback port, the user approves it on
//! the website's /app/authorize page, and the browser comes back with a
//! one-time code that is exchanged for an app session token.

use anyhow::{Context, anyhow, bail};
use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::time::Duration;
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::TcpListener;
use url::Url;

use crate::{User, http_client};

/// How long to wait for the user to approve the app in the browser.
const APPROVAL_TIMEOUT: Duration = Duration::from_secs(10 * 60);

const DONE_PAGE: &str = "<!doctype html><meta charset=utf-8><title>CupStream</title>\
<body style=\"font-family:sans-serif;background:#111;color:#eee;display:grid;place-items:center;height:90vh\">\
<p>You can close this tab and go back to the CupStream app.</p>";

/// The result of signing in.
#[derive(Clone, Debug, Deserialize)]
pub struct SignedIn {
    /// The app session token: keep it secret (in the system keychain).
    pub token: String,
    pub user: User,
}

/// A sign-in in progress: open `url` in the browser, then `finish`.
pub struct SignIn {
    pub url: Url,
    server: Url,
    listener: TcpListener,
    redirect_uri: String,
    state: String,
    verifier: String,
}

fn random_token(bytes: usize) -> String {
    let mut b = vec![0u8; bytes];
    getrandom::fill(&mut b).expect("the system has a random number generator");
    URL_SAFE_NO_PAD.encode(b)
}

impl SignIn {
    /// Starts listening on a loopback port and builds the URL to open.
    pub async fn start(server: &str, device_name: &str) -> anyhow::Result<Self> {
        let server = Url::parse(server).context("invalid server URL")?;
        let listener = TcpListener::bind("127.0.0.1:0").await?;
        let port = listener.local_addr()?.port();
        let redirect_uri = format!("http://127.0.0.1:{port}/callback");
        let state = random_token(16);
        let verifier = random_token(32); // 43 characters
        let challenge = URL_SAFE_NO_PAD.encode(Sha256::digest(verifier.as_bytes()));

        let mut url = server.join("/app/authorize")?;
        url.query_pairs_mut()
            .append_pair("redirect_uri", &redirect_uri)
            .append_pair("state", &state)
            .append_pair("code_challenge", &challenge)
            .append_pair("code_challenge_method", "S256")
            .append_pair("device_name", device_name);
        Ok(Self {
            url,
            server,
            listener,
            redirect_uri,
            state,
            verifier,
        })
    }

    /// Waits for the browser to come back, then exchanges the code.
    pub async fn finish(self) -> anyhow::Result<SignedIn> {
        let code = tokio::time::timeout(APPROVAL_TIMEOUT, self.wait_for_code())
            .await
            .map_err(|_| anyhow!("sign-in timed out"))??;

        #[derive(Serialize)]
        #[serde(rename_all = "camelCase")]
        struct Req<'a> {
            code: &'a str,
            code_verifier: &'a str,
            redirect_uri: &'a str,
        }
        let resp = http_client()?
            .post(self.server.join("/api/app/token")?)
            .json(&Req {
                code: &code,
                code_verifier: &self.verifier,
                redirect_uri: &self.redirect_uri,
            })
            .send()
            .await?;
        if !resp.status().is_success() {
            let status = resp.status();
            let body = resp.text().await.unwrap_or_default();
            bail!("exchanging the sign-in code failed ({status}): {body}");
        }
        Ok(resp.json().await?)
    }

    /// Answers requests on the loopback port until one carries our state.
    async fn wait_for_code(&self) -> anyhow::Result<String> {
        loop {
            let (mut sock, _) = self.listener.accept().await?;
            let mut buf = vec![0u8; 8192];
            let n = sock.read(&mut buf).await?;
            let head = String::from_utf8_lossy(&buf[..n]);
            let Some(target) = head.lines().next().and_then(|l| l.split(' ').nth(1)) else {
                continue;
            };
            let Ok(url) = Url::parse(&format!("http://127.0.0.1{target}")) else {
                continue;
            };
            let param = |name: &str| {
                url.query_pairs()
                    .find(|(k, _)| k == name)
                    .map(|(_, v)| v.into_owned())
            };
            if url.path() != "/callback" || param("state").as_deref() != Some(self.state.as_str()) {
                let _ = sock
                    .write_all(
                        b"HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\nConnection: close\r\n\r\n",
                    )
                    .await;
                continue;
            }
            let _ = sock
                .write_all(
                    format!(
                        "HTTP/1.1 200 OK\r\nContent-Type: text/html; charset=utf-8\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{DONE_PAGE}",
                        DONE_PAGE.len()
                    )
                    .as_bytes(),
                )
                .await;
            if let Some(err) = param("error") {
                bail!("sign-in was refused in the browser ({err})");
            }
            return param("code").ok_or_else(|| anyhow!("the browser came back without a code"));
        }
    }
}
