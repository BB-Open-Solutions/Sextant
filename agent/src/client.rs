//! The check-in client: one POST per beat to /api/checkin, authenticated
//! with the per-device credential. The server's status codes drive the
//! agent's behaviour, including permanent retirement (410).

use serde::Serialize;
use std::time::Duration;

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
    #[serde(skip_serializing_if = "Option::is_none")]
    pub facts: Option<&'a serde_json::Value>,
}

/// Outcome of one beat, as far as the loop needs to know.
#[derive(Debug, PartialEq)]
pub enum Outcome {
    /// Accepted (204).
    Ok,
    /// The device is retired (410): stop for good, a human must act.
    Retired,
    /// Credential rejected (401): keep trying, it may be re-issued.
    Unauthorized,
    /// Anything else (5xx, network): transient, retry next beat.
    Transient(String),
}

/// Client posts check-ins.
pub struct Client {
    agent: ureq::Agent,
    url: String,
    credential: String,
}

impl Client {
    pub fn new(base_url: &str, credential: &str) -> Client {
        Client {
            agent: ureq::AgentBuilder::new()
                .timeout(Duration::from_secs(15))
                .build(),
            url: format!("{base_url}/api/checkin"),
            credential: credential.to_string(),
        }
    }

    /// send posts one check-in and classifies the response.
    pub fn send(&self, body: &CheckIn) -> Outcome {
        let res = self
            .agent
            .post(&self.url)
            .set("Authorization", &format!("Bearer {}", self.credential))
            .send_json(body);
        match res {
            Ok(_) => Outcome::Ok,
            Err(ureq::Error::Status(401, _)) => Outcome::Unauthorized,
            Err(ureq::Error::Status(410, _)) => Outcome::Retired,
            Err(ureq::Error::Status(code, resp)) => Outcome::Transient(format!(
                "server returned {code}: {}",
                resp.into_string().unwrap_or_default().trim()
            )),
            Err(e) => Outcome::Transient(e.to_string()),
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
            facts: None,
        };
        assert_eq!(c.send(&body), Outcome::Ok);
        assert_eq!(c.send(&body), Outcome::Unauthorized);
        assert_eq!(c.send(&body), Outcome::Retired);
        assert!(matches!(c.send(&body), Outcome::Transient(_)));

        let bodies = handle.join().unwrap();
        // Wire shape matches the server contract; absent fields are omitted.
        assert_eq!(
            bodies[0],
            r#"{"tag":"lt-1","revision":"rev-9","phase":"running"}"#
        );
    }
}
