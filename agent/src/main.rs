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
    // wipe_challenge is the signed nonce + issued-at from a fresh wipe intent
    // (design 0004), remembered so it can be echoed with the wipe ack for the
    // server's replay check. Set when a fresh wipe is acted on, sent with the
    // next wipe-outcome ack.
    let mut wipe_challenge: Option<(String, i64)> = None;
    // consecutive_failures drives exponential backoff: when the console is
    // down, a 10k-device fleet must not keep hammering it at full cadence
    // and then stampede its recovery.
    let mut consecutive_failures: u32 = 0;
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
        // A wipe-outcome ack carries back the signed nonce + timestamp the
        // wipe intent arrived with, so the server can verify it answers an
        // instruction it issued recently (design 0004).
        let (ack_nonce, ack_ts) = match (&wipe_challenge, ack.starts_with("wipe")) {
            (Some((n, t)), true) => (n.as_str(), *t),
            _ => ("", 0),
        };
        let usage = collect::collect_usage();
        let beat = CheckIn {
            tag: &cfg.tag,
            revision: &revision,
            phase: "running",
            error: None,
            sb: post.sb,
            tpm2: post.tpm2,
            ack: &ack,
            ack_nonce,
            ack_ts,
            facts: facts.as_ref(),
            usage: Some(&usage),
        };
        let outcome = client.send(&beat);
        if beat_accepted(&outcome) {
            consecutive_failures = 0;
            // The wipe ack (with its nonce) reached the server: the challenge
            // has done its job, drop it so a later beat cannot resend it.
            if ack.starts_with("wipe") {
                wipe_challenge = None;
            }
        } else {
            pending_ack = ack;
            consecutive_failures = consecutive_failures.saturating_add(1);
        }
        match outcome {
            Outcome::Ok => {
                if facts.is_some() {
                    last_facts = Some(Instant::now());
                }
            }
            Outcome::Intent { intent, nonce, ts } => {
                if facts.is_some() {
                    last_facts = Some(Instant::now());
                }
                // The destructive wipe only acts when fresh (design 0004): a
                // replayed old response carries a stale timestamp and is
                // ignored, so it cannot re-trigger an erase. The signed nonce
                // is remembered to echo in the ack for the server's check.
                if intent == "wipe" {
                    match ts {
                        Some(t) if wipe_is_fresh(t) => {
                            wipe_challenge = nonce.map(|n| (n, t));
                            action::react(&posture::default_root(), &intent);
                        }
                        _ => {
                            eprintln!(
                                "sextant-agent: wipe intent ignored: missing or stale \
                                 timestamp (replay guard, design 0004)"
                            );
                        }
                    }
                } else {
                    // Spool it for the privileged executor; the real outcome is
                    // reported on a later beat via action::executed_ack.
                    action::react(&posture::default_root(), &intent);
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
        let wait = backoff(cfg.interval, consecutive_failures);
        std::thread::sleep(wait + jitter(wait));
    }
}

/// backoff stretches the beat interval exponentially while check-ins keep
/// failing (x1, x2, x4, ... capped at 16x), so an unreachable console is not
/// hammered at full fleet cadence and its recovery is not a stampede. The
/// first successful beat resets to the normal interval. Jitter is applied
/// over the STRETCHED interval, so spread grows with it.
fn backoff(interval: Duration, consecutive_failures: u32) -> Duration {
    let factor = 1u32 << consecutive_failures.min(4); // 1..16
    interval.saturating_mul(factor)
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
    matches!(outcome, Outcome::Ok | Outcome::Intent { .. })
}

/// WIPE_WINDOW_SECS bounds how recent a wipe instruction's timestamp must be
/// for the agent to act on it (design 0004: < 15 min). A replayed old
/// response falls outside the window and is ignored.
const WIPE_WINDOW_SECS: i64 = 15 * 60;

/// wipe_is_fresh reports whether a wipe instruction's issued-at second is
/// recent enough to act on. A timestamp in the future, or older than the
/// window, is refused.
fn wipe_is_fresh(ts: i64) -> bool {
    let now = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_secs() as i64)
        .unwrap_or(0);
    let age = now - ts;
    (0..=WIPE_WINDOW_SECS).contains(&age)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn only_ok_and_intent_count_as_accepted() {
        assert!(beat_accepted(&Outcome::Ok));
        assert!(beat_accepted(&Outcome::Intent {
            intent: "lock".to_string(),
            nonce: None,
            ts: None,
        }));

        assert!(!beat_accepted(&Outcome::Retired));
        assert!(!beat_accepted(&Outcome::Unauthorized));
        assert!(!beat_accepted(&Outcome::Transient("timeout".to_string())));
    }

    #[test]
    fn wipe_freshness_bounds_the_window() {
        let now = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .unwrap()
            .as_secs() as i64;
        assert!(wipe_is_fresh(now)); // issued now
        assert!(wipe_is_fresh(now - WIPE_WINDOW_SECS + 5)); // within window
        assert!(!wipe_is_fresh(now - WIPE_WINDOW_SECS - 5)); // stale = replay
        assert!(!wipe_is_fresh(now + 60)); // future = reject
    }

    #[test]
    fn backoff_doubles_and_caps_at_sixteen_x() {
        let base = Duration::from_secs(60);
        assert_eq!(backoff(base, 0), base); // healthy: normal cadence
        assert_eq!(backoff(base, 1), base * 2);
        assert_eq!(backoff(base, 2), base * 4);
        assert_eq!(backoff(base, 4), base * 16);
        assert_eq!(backoff(base, 10), base * 16); // capped
        assert_eq!(backoff(base, u32::MAX), base * 16); // no overflow
    }
}
