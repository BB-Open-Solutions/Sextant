//! The check-in client: one POST per beat to /api/checkin, authenticated
//! with the per-device credential. The server's status codes drive the
//! agent's behaviour, including permanent retirement (410).

use serde::Serialize;
use std::time::Duration;

/// is_zero lets serde skip the ack timestamp when unset (0).
fn is_zero(n: &i64) -> bool {
    *n == 0
}

/// CheckIn mirrors the server's observed.CheckIn contract.
#[derive(Serialize)]
pub struct CheckIn<'a> {
    pub tag: &'a str,
    pub revision: &'a str,
    pub phase: &'a str,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub error: Option<&'a str>,
    #[serde(skip_serializing_if = "str::is_empty")]
    pub sb: &'a str,
    #[serde(skip_serializing_if = "str::is_empty")]
    pub tpm2: &'a str,
    #[serde(skip_serializing_if = "str::is_empty")]
    pub ack: &'a str,
    /// ackNonce/ackTs echo the wipe replay nonce (design 0004) so the server
    /// can confirm a wipe ack answers an instruction it issued recently. Set
    /// only on a wipe ack; empty otherwise.
    #[serde(rename = "ackNonce", skip_serializing_if = "str::is_empty")]
    pub ack_nonce: &'a str,
    #[serde(rename = "ackTs", skip_serializing_if = "is_zero")]
    pub ack_ts: i64,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub facts: Option<&'a serde_json::Value>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub usage: Option<&'a crate::collect::Usage>,
    /// health is systemd's verdict on this machine. A revision says what the
    /// device MEANT to run; this says whether it works, and the console needs
    /// the second to be able to veto the first (e2e5, 2026-08-04).
    #[serde(skip_serializing_if = "Option::is_none")]
    pub health: Option<&'a crate::collect::Health>,
    /// recoveryKey carries a provisioning-minted LUKS recovery key exactly
    /// until the server confirms sealing it (design 0009); None otherwise.
    #[serde(rename = "recoveryKey", skip_serializing_if = "Option::is_none")]
    pub recovery_key: Option<&'a str>,
}

/// Outcome of one beat, as far as the loop needs to know.
#[derive(Debug, PartialEq)]
pub enum Outcome {
    /// Accepted (204).
    Ok,
    /// Accepted, and the server returned a pending remote-action intent
    /// (200 with an intent body) for the device to act on locally. A wipe
    /// carries a signed nonce + issued-at second (design 0004): the agent
    /// checks freshness and echoes them in its ack.
    Intent {
        intent: String,
        nonce: Option<String>,
        ts: Option<i64>,
    },
    /// The device is retired (410): stop for good, a human must act.
    Retired,
    /// Credential rejected (401): keep trying, it may be re-issued.
    Unauthorized,
    /// Anything else (5xx, network): transient, retry next beat.
    Transient(String),
}

/// Client posts check-ins and diagnostics uploads.
pub struct Client {
    agent: ureq::Agent,
    url: String,
    base_url: String,
    credential: String,
}

impl Client {
    pub fn new(base_url: &str, credential: &str) -> Client {
        Client {
            agent: ureq::AgentBuilder::new()
                .timeout(Duration::from_secs(15))
                .build(),
            url: format!("{base_url}/api/checkin"),
            base_url: base_url.to_string(),
            credential: credential.to_string(),
        }
    }

    /// upload_diagnostics posts the collected bundle (design 0010). Returns
    /// true only on a 2xx - the caller deletes the local copy on that, so a
    /// down console means retry next beat, never a lost bundle.
    pub fn upload_diagnostics(&self, tag: &str, bundle: &[u8]) -> bool {
        let url = format!("{}/api/device/{}/diagnostics", self.base_url, tag);
        match self
            .agent
            .post(&url)
            .set("Authorization", &format!("Bearer {}", self.credential))
            .set("Content-Type", "application/gzip")
            .send_bytes(bundle)
        {
            Ok(_) => true,
            Err(e) => {
                eprintln!("sextant-agent: diagnostics upload failed: {e}");
                false
            }
        }
    }

    /// send posts one check-in and classifies the response. The second
    /// return reports whether the server confirmed sealing this beat's
    /// recovery key (the X-Recovery-Key-Stored header, design 0009) - the
    /// caller deletes the local copy only on that confirmation.
    pub fn send(&self, body: &CheckIn) -> (Outcome, bool) {
        let res = self
            .agent
            .post(&self.url)
            .set("Authorization", &format!("Bearer {}", self.credential))
            .send_json(body);
        match res {
            Ok(resp) => {
                let recovery_stored = resp.header("X-Recovery-Key-Stored").is_some();
                // 204 = nothing pending; 200 carries a remote-action intent.
                if resp.status() == 200 {
                    if let Ok(doc) = resp.into_json::<serde_json::Value>() {
                        if let Some(intent) = doc.get("intent").and_then(|v| v.as_str()) {
                            if !intent.is_empty() {
                                return (
                                    Outcome::Intent {
                                        intent: intent.to_string(),
                                        nonce: doc
                                            .get("nonce")
                                            .and_then(|v| v.as_str())
                                            .map(str::to_string),
                                        ts: doc.get("ts").and_then(|v| v.as_i64()),
                                    },
                                    recovery_stored,
                                );
                            }
                        }
                    }
                }
                (Outcome::Ok, recovery_stored)
            }
            Err(ureq::Error::Status(401, _)) => (Outcome::Unauthorized, false),
            Err(ureq::Error::Status(410, _)) => (Outcome::Retired, false),
            Err(ureq::Error::Status(code, resp)) => (
                Outcome::Transient(format!(
                    "server returned {code}: {}",
                    resp.into_string().unwrap_or_default().trim()
                )),
                false,
            ),
            Err(e) => (Outcome::Transient(e.to_string()), false),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::{BufRead, BufReader, Read, Write};
    use std::net::TcpListener;
    use std::thread;

    /// tiny_server answers each connection with the given status once.
    fn tiny_server(responses: Vec<u16>) -> (String, thread::JoinHandle<Vec<String>>) {
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let addr = listener.local_addr().unwrap();
        let handle = thread::spawn(move || {
            let mut bodies = Vec::new();
            for status in responses {
                let (mut stream, _) = listener.accept().unwrap();
                let mut reader = BufReader::new(stream.try_clone().unwrap());
                let mut line = String::new();
                let mut len = 0usize;
                // Read request head; capture Content-Length.
                loop {
                    line.clear();
                    reader.read_line(&mut line).unwrap();
                    if let Some(v) = line
                        .to_ascii_lowercase()
                        .strip_prefix("content-length:")
                        .map(str::trim)
                        .and_then(|v| v.parse().ok())
                    {
                        len = v;
                    }
                    if line == "\r\n" {
                        break;
                    }
                }
                let mut body = vec![0u8; len];
                reader.read_exact(&mut body).unwrap();
                bodies.push(String::from_utf8_lossy(&body).into_owned());
                let reason = match status {
                    204 => "No Content",
                    401 => "Unauthorized",
                    410 => "Gone",
                    _ => "Error",
                };
                write!(
                    stream,
                    "HTTP/1.1 {status} {reason}\r\ncontent-length: 0\r\nconnection: close\r\n\r\n"
                )
                .unwrap();
            }
            bodies
        });
        (format!("http://127.0.0.1:{}", addr.port()), handle)
    }

    #[test]
    fn send_classifies_statuses_and_serializes_body() {
        let (url, handle) = tiny_server(vec![204, 401, 410, 503]);
        let c = Client::new(&url, "sxt_test_secret");
        let body = CheckIn {
            tag: "lt-1",
            revision: "rev-9",
            phase: "running",
            error: None,
            sb: "",
            tpm2: "",
            ack: "",
            ack_nonce: "",
            ack_ts: 0,
            facts: None,
            usage: None,
            health: None,
            recovery_key: None,
        };
        // The plain server sends no X-Recovery-Key-Stored header, so the
        // stored flag stays false on every classification.
        assert_eq!(c.send(&body), (Outcome::Ok, false));
        assert_eq!(c.send(&body), (Outcome::Unauthorized, false));
        assert_eq!(c.send(&body), (Outcome::Retired, false));
        assert!(matches!(c.send(&body), (Outcome::Transient(_), false)));

        let bodies = handle.join().unwrap();
        // Wire shape matches the server contract; absent fields are omitted.
        assert_eq!(
            bodies[0],
            r#"{"tag":"lt-1","revision":"rev-9","phase":"running"}"#
        );
    }
}
