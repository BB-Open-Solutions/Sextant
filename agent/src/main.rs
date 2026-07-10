//! sextant-agent: the device side of the Sextant control plane (ADR 0010).
//! Declarative model: comin converges configuration; this agent only
//! OBSERVES and REPORTS - deployed revision every beat, hardware facts on
//! start and daily. It never applies changes and holds no privileges
//! beyond reading /run/current-system and running nixos-facter.
//!
//! Exit codes: 0 ok/once, 2 config error, 3 retired (permanent - the
//! systemd unit must not restart on this).

mod client;
mod collect;
mod config;

use client::{CheckIn, Client, Outcome};
use std::collections::hash_map::RandomState;
use std::hash::{BuildHasher, Hasher};
use std::process::ExitCode;
use std::time::{Duration, Instant};

fn main() -> ExitCode {
    let once = std::env::args().any(|a| a == "--once");
    let cfg = match config::Config::from_env() {
        Ok(c) => c,
        Err(e) => {
            eprintln!("sextant-agent: {e}");
            return ExitCode::from(2);
        }
    };
    let client = Client::new(&cfg.url, &cfg.credential);
    eprintln!(
        "sextant-agent: reporting {} to {} every {}s",
        cfg.tag,
        cfg.url,
        cfg.interval.as_secs()
    );

    let mut last_facts: Option<Instant> = None;
    loop {
        // Facts ride along on the first beat and then per facts_interval.
        let due = last_facts.is_none_or(|t| t.elapsed() >= cfg.facts_interval);
        let facts = if due {
            collect::facts(&cfg.facter)
        } else {
            None
        };

        let revision = collect::revision();
        let beat = CheckIn {
            tag: &cfg.tag,
            revision: &revision,
            phase: "running",
            error: None,
            facts: facts.as_ref(),
        };
        match client.send(&beat) {
            Outcome::Ok => {
                if facts.is_some() {
                    last_facts = Some(Instant::now());
                }
            }
            Outcome::Retired => {
                eprintln!("sextant-agent: device is retired; stopping permanently");
                return ExitCode::from(3);
            }
            Outcome::Unauthorized => {
                // Credential may have been rotated server-side; keep beating
                // so a re-issued credential file picks up on restart, and be
                // loud in the journal meanwhile.
                eprintln!("sextant-agent: credential rejected (401); check re-issue");
            }
            Outcome::Transient(msg) => {
                eprintln!("sextant-agent: check-in failed: {msg}");
            }
        }

        if once {
            return ExitCode::SUCCESS;
        }
        std::thread::sleep(cfg.interval + jitter(cfg.interval));
    }
}

/// jitter spreads beats up to 10% of the interval so a fleet rebooting
/// together does not thundering-herd the console.
fn jitter(interval: Duration) -> Duration {
    let max_ms = (interval.as_millis() / 10).max(1) as u64;
    let r = RandomState::new().build_hasher().finish();
    Duration::from_millis(r % max_ms)
}
