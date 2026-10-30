//! The app session token, kept in the system keychain (Secret Service on
//! Linux, Credential Manager on Windows), one entry per server.

const SERVICE: &str = "CupStream";

fn entry(server: &str) -> keyring::Result<keyring::Entry> {
    keyring::Entry::new(SERVICE, server)
}

pub fn load(server: &str) -> Option<String> {
    match entry(server).and_then(|e| e.get_password()) {
        Ok(t) => Some(t),
        Err(keyring::Error::NoEntry) => None,
        Err(e) => {
            tracing::warn!("reading the keychain failed: {e}");
            None
        }
    }
}

pub fn store(server: &str, token: &str) -> anyhow::Result<()> {
    entry(server)?.set_password(token)?;
    Ok(())
}

pub fn delete(server: &str) {
    if let Err(e) = entry(server).and_then(|e| e.delete_credential())
        && !matches!(e, keyring::Error::NoEntry)
    {
        tracing::warn!("removing the token from the keychain failed: {e}");
    }
}
