//! Agent configuration: environment plus the systemd credential file.
//! The credential (per-device, ADR 0008) never sits in the nix store or
//! the environment; systemd's LoadCredential hands us a file path.

use std::env;
use std::fmt;
use std::fs;
use std::time::Duration;

/// Configuration resolved at startup.
///
/// Debug is implemented by hand (below) instead of derived: the credential
/// field must never reach a log line, even from future debug statements.
#[derive(Clone)]
pub struct Config {
    /// Console base URL, e.g. "https://console.bb-open.com".
    pub url: String,
    /// Device asset tag as enrolled in the fleet.
    pub tag: String,
    /// The per-device credential (bearer secret), read from a file.
    pub credential: String,
    /// Seconds between check-ins in daemon mode.
    pub interval: Duration,
    /// Path to the nixos-facter binary; empty disables facts reporting.
    pub facter: String,
    /// Seconds between facts uploads (facts are large; daily by default).
    pub facts_interval: Duration,
}

impl fmt::Debug for Config {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("Config")
            .field("url", &self.url)
            .field("tag", &self.tag)
            .field("credential", &"<redacted>")
            .field("interval", &self.interval)
            .field("facter", &self.facter)
            .field("facts_interval", &self.facts_interval)
            .finish()
    }
}

/// A configuration error names the missing/broken piece exactly.
#[derive(Debug)]
pub struct ConfigError(String);

impl fmt::Display for ConfigError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "config: {}", self.0)
    }
}

impl std::error::Error for ConfigError {}

fn err(msg: impl Into<String>) -> ConfigError {
    ConfigError(msg.into())
}

impl Config {
    /// Load from the environment. Required: SEXTANT_URL, SEXTANT_TAG and a
    /// credential (SEXTANT_CREDENTIAL_FILE, or CREDENTIALS_DIRECTORY/credential
    /// when running under systemd LoadCredential).
    pub fn from_env() -> Result<Config, ConfigError> {
        let get = |k: &str| env::var(k).unwrap_or_default();

        let url = get("SEXTANT_URL");
        if url.is_empty() {
            return Err(err("SEXTANT_URL is required"));
        }
        let url = url.trim_end_matches('/').to_string();
        if !url.starts_with("https://") && !is_loopback_http(&url) {
            return Err(err("SEXTANT_URL must be https (or localhost for tests)"));
        }

        let tag = get("SEXTANT_TAG");
        if tag.is_empty() || tag.len() > 63 {
            return Err(err("SEXTANT_TAG is required (max 63 chars)"));
        }

        let cred_path = match get("SEXTANT_CREDENTIAL_FILE") {
            p if !p.is_empty() => p,
            _ => match get("CREDENTIALS_DIRECTORY") {
                d if !d.is_empty() => format!("{d}/credential"),
                _ => return Err(err(
                    "no credential: set SEXTANT_CREDENTIAL_FILE or run under systemd LoadCredential",
                )),
            },
        };
        let credential = fs::read_to_string(&cred_path)
            .map_err(|e| err(format!("credential file {cred_path}: {e}")))?
            .trim()
            .to_string();
        if credential.is_empty() {
            return Err(err(format!("credential file {cred_path} is empty")));
        }

        Ok(Config {
            url,
            tag,
            credential,
            interval: parse_seconds(&get("SEXTANT_INTERVAL"), 60)?,
            facter: get("SEXTANT_FACTER"),
            facts_interval: parse_seconds(&get("SEXTANT_FACTS_INTERVAL"), 24 * 3600)?,
        })
    }
}

/// is_loopback_http allows plaintext http only for a genuine loopback host,
/// so a prefix trick like "http://localhost.evil.com" cannot leak the bearer
/// credential to a remote host in cleartext. The host is the substring after
/// "http://" up to the next '/' or ':' (bracketed for IPv6), and must be
/// exactly "localhost", "127.0.0.1" or "[::1]" - not merely start with one.
fn is_loopback_http(url: &str) -> bool {
    let Some(rest) = url.strip_prefix("http://") else {
        return false;
    };
    let host = if let Some(after_bracket) = rest.strip_prefix('[') {
        match after_bracket.find(']') {
            Some(end) => {
                let host = &rest[..end + 2]; // include the leading '[' and the ']'
                                             // Reject "[::1].evil.example": whatever follows the closing
                                             // bracket must start a port or path, not extend the host.
                match rest[end + 2..].chars().next() {
                    None | Some('/') | Some(':') => host,
                    Some(_) => return false,
                }
            }
            None => return false,
        }
    } else {
        rest.split(['/', ':']).next().unwrap_or("")
    };
    host == "localhost" || host == "127.0.0.1" || host == "[::1]"
}

/// parse_seconds reads a positive integer seconds value, with a default.
fn parse_seconds(s: &str, default: u64) -> Result<Duration, ConfigError> {
    if s.is_empty() {
        return Ok(Duration::from_secs(default));
    }
    match s.parse::<u64>() {
        Ok(n) if n > 0 => Ok(Duration::from_secs(n)),
        _ => Err(err(format!("expected positive seconds, got {s:?}"))),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parse_seconds_defaults_and_rejects() {
        assert_eq!(parse_seconds("", 60).unwrap(), Duration::from_secs(60));
        assert_eq!(parse_seconds("90", 60).unwrap(), Duration::from_secs(90));
        assert!(parse_seconds("0", 60).is_err());
        assert!(parse_seconds("-5", 60).is_err());
        assert!(parse_seconds("soon", 60).is_err());
    }

    #[test]
    fn loopback_http_requires_an_exact_host_match() {
        // Genuine loopback, with and without a port/path, is allowed.
        assert!(is_loopback_http("http://localhost"));
        assert!(is_loopback_http("http://localhost:8080"));
        assert!(is_loopback_http("http://localhost/status"));
        assert!(is_loopback_http("http://127.0.0.1"));
        assert!(is_loopback_http("http://127.0.0.1:9000"));
        assert!(is_loopback_http("http://[::1]"));
        assert!(is_loopback_http("http://[::1]:9000"));

        // A loopback-looking prefix on a different host must not sneak past
        // as plaintext http - the whole point of the fix.
        assert!(!is_loopback_http("http://localhost.evil.com"));
        assert!(!is_loopback_http("http://127.0.0.1.evil.example"));
        assert!(!is_loopback_http("http://[::1].evil.example"));
        assert!(!is_loopback_http("https://localhost"));
        assert!(!is_loopback_http("http://evil.com"));
    }
}
