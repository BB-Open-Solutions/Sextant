//! sextant-agent: the device side of the Sextant control plane (ADR 0010).
//! Declarative model: comin converges configuration; this agent only
//! OBSERVES and REPORTS - deployed revision every beat, hardware facts on
//! start and daily. It never applies changes and holds no privileges
//! beyond reading /run/current-system and running nixos-facter.
//!
//! Exit codes: 0 ok/once, 2 config error, 3 retired (permanent - the
//! systemd unit must not restart on this).

mod action;
mod client;
mod collect;
mod config;
mod posture;

use client::{CheckIn, Client, Outcome};
use std::process::ExitCode;
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

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
    // pending_ack is echoed on the next beat once an intent has been acted
    // on, so the console can show delivered vs armed.
    let mut pending_ack = String::new();
    loop {
        // Facts ride along on the first beat and then per facts_interval.
        let due = last_facts.is_none_or(|t| t.elapsed() >= cfg.facts_interval);
        let facts = if due {
            collect::facts(&cfg.facter)
        } else {
            None
        };

        let revision = collect::revision();
        let post = posture::probe(&posture::default_root());
        // Forward the executor's recorded outcome (executed/refused/failed) if
        // one is waiting - the real ack, distinct from merely having spooled.
        if let Some(outcome) = action::executed_ack(&posture::default_root()) {
            pending_ack = outcome;
        }
        // Take the pending ack for this beat, but do not treat it as
        // delivered yet: it is only restored to pending_ack below if the
        // send does not come back accepted, so a Transient/Unauthorized
        // failure never drops an outcome the console has not yet seen.
        let ack = std::mem::take(&mut pending_ack);
        let usage = collect::collect_usage();
        let beat = CheckIn {
            tag: &cfg.tag,
            revision: &revision,
            phase: "running",
            error: None,
            sb: post.sb,
            tpm2: post.tpm2,
            ack: &ack,
            facts: facts.as_ref(),
            usage: Some(&usage),
        };
        let outcome = client.send(&beat);
        if !beat_accepted(&outcome) {
            pending_ack = ack;
        }
        match outcome {
            Outcome::Ok => {
                if facts.is_some() {
                    last_facts = Some(Instant::now());
                }
            }
            Outcome::Intent(intent) => {
                if facts.is_some() {
                    last_facts = Some(Instant::now());
                }
                // Spool it for the privileged executor; the real outcome is
                // reported on a later beat via action::executed_ack.
                action::react(&posture::default_root(), &intent);
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
    // Spread beats using the sub-second wall-clock as a cheap, dependency-free
    // source of variation - jitter wants spread, not cryptographic randomness.
    let nanos = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| u64::from(d.subsec_nanos()))
        .unwrap_or(0);
    Duration::from_millis(nanos % max_ms)
}

/// beat_accepted reports whether the server actually received and processed
/// the beat that carried the ack. Only Ok and Intent mean the server saw it;
/// Retired, Unauthorized and Transient all mean the ack this beat carried was
/// never confirmed delivered, so the caller must retain it for a later beat
/// rather than dropping it with std::mem::take.
fn beat_accepted(outcome: &Outcome) -> bool {
    matches!(outcome, Outcome::Ok | Outcome::Intent(_))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn only_ok_and_intent_count_as_accepted() {
        assert!(beat_accepted(&Outcome::Ok));
        assert!(beat_accepted(&Outcome::Intent("lock".to_string())));

        assert!(!beat_accepted(&Outcome::Retired));
        assert!(!beat_accepted(&Outcome::Unauthorized));
        assert!(!beat_accepted(&Outcome::Transient("timeout".to_string())));
    }
}
