//! Remote-action execution (design 0004). The agent receives an intent in
//! its check-in response and reacts LOCALLY - no inbound control channel.
//!
//! Privilege boundary: the unprivileged agent drops a validated intent
//! file into a spool directory; a separate root oneshot (sextant-actd,
//! part of the NixOS module) executes it. The lock path is implemented
//! here for the flag it can own; the DESTRUCTIVE wipe execution is
//! deliberately NOT implemented in this build - the agent only records
//! the request and returns the ack, leaving the LUKS erase to the root
//! unit that a later change (Opus handoff) adds. Until then a wipe intent
//! is spooled and acknowledged but not carried out, and that is logged
//! loudly so it is never mistaken for done.

use std::fs;
use std::path::Path;

/// SPOOL is where the agent drops an intent for the root executor.
pub const SPOOL: &str = "/run/sextant-intent";

/// LOCK_FLAG persists a lock across reboot; a tiny systemd unit re-locks
/// when it is present.
pub const LOCK_FLAG: &str = "/var/lib/sextant-agent/locked";

/// react handles an intent from the check-in response and returns the ack
/// to echo on the next beat (empty if the intent was unrecognised).
pub fn react(root: &Path, intent: &str) -> String {
    match intent {
        "lock" => {
            lock(root);
            "lock".to_string()
        }
        "wipe" => {
            spool(root, "wipe");
            eprintln!(
                "sextant-agent: WIPE intent received and spooled; \
                 destructive execution is handled by the root executor, \
                 NOT this agent build"
            );
            "wipe".to_string()
        }
        other => {
            eprintln!("sextant-agent: unknown intent {other:?}, ignored");
            String::new()
        }
    }
}

/// lock writes the persistent lock flag and spools a lock request for the
/// root unit to lock active sessions. Creating the flag is reversible: the
/// console clears the intent, the next beat carries no lock, and a
/// separate path (out of scope here) removes it.
fn lock(root: &Path) {
    let flag = root.join(LOCK_FLAG.trim_start_matches('/'));
    if let Some(parent) = flag.parent() {
        let _ = fs::create_dir_all(parent);
    }
    if let Err(e) = fs::write(&flag, b"locked\n") {
        eprintln!("sextant-agent: could not write lock flag: {e}");
    }
    spool(root, "lock");
}

/// spool drops an intent marker for the root executor to pick up. The file
/// name is fixed per intent so repeated beats do not pile up.
fn spool(root: &Path, intent: &str) {
    let dir = root.join(SPOOL.trim_start_matches('/'));
    if let Err(e) = fs::create_dir_all(&dir) {
        eprintln!("sextant-agent: could not create spool dir: {e}");
        return;
    }
    if let Err(e) = fs::write(dir.join(format!("{intent}.intent")), b"") {
        eprintln!("sextant-agent: could not spool {intent}: {e}");
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn tmp(name: &str) -> std::path::PathBuf {
        let p = std::env::temp_dir().join(format!("sxt-action-{name}"));
        let _ = fs::remove_dir_all(&p);
        fs::create_dir_all(&p).unwrap();
        p
    }

    #[test]
    fn lock_writes_flag_and_spools() {
        let root = tmp("lock");
        let ack = react(&root, "lock");
        assert_eq!(ack, "lock");
        assert!(root.join(LOCK_FLAG.trim_start_matches('/')).exists());
        assert!(root
            .join(SPOOL.trim_start_matches('/'))
            .join("lock.intent")
            .exists());
        let _ = fs::remove_dir_all(&root);
    }

    #[test]
    fn wipe_spools_but_does_not_destroy() {
        let root = tmp("wipe");
        let ack = react(&root, "wipe");
        assert_eq!(ack, "wipe");
        // Only a spool marker; the agent itself performs no destruction.
        assert!(root
            .join(SPOOL.trim_start_matches('/'))
            .join("wipe.intent")
            .exists());
        let _ = fs::remove_dir_all(&root);
    }

    #[test]
    fn unknown_intent_ignored() {
        let root = tmp("unknown");
        assert_eq!(react(&root, "explode"), "");
        let _ = fs::remove_dir_all(&root);
    }
}
