//! Remote-action execution (design 0004). The agent receives an intent in
//! its check-in response and reacts LOCALLY - no inbound control channel.
//!
//! Privilege boundary: the unprivileged agent drops a validated intent
//! file into a spool directory; a separate root oneshot (sextant-actd,
//! deploy/nixos/actd.nix) executes it. The agent never holds the privilege
//! to lock a session or erase a disk - it only records the request and
//! returns the ack. The root executor carries out lock always, and wipe
//! only when that host is explicitly armed (services.sextant-actd.armWipe)
//! and the lock interlock is satisfied; an unarmed host refuses and logs
//! loudly, so a wipe is never carried out by accident.

use std::fs;
use std::path::Path;

/// SPOOL is where the agent drops an intent for the root executor.
pub const SPOOL: &str = "/run/sextant-intent";

/// LOCK_FLAG persists a lock across reboot; a tiny systemd unit re-locks
/// when it is present.
pub const LOCK_FLAG: &str = "/var/lib/sextant-agent/locked";

/// react handles an intent from the check-in response by spooling it for the
/// privileged executor. It does NOT return an ack: spooling is not execution.
/// The real outcome is reported later via `executed_ack`, once the root
/// executor has run and recorded it - so the console never mistakes a
/// delivered request for a completed action.
pub fn react(root: &Path, intent: &str) {
    match intent {
        "lock" => lock(root),
        "wipe" => {
            spool(root, "wipe");
            eprintln!(
                "sextant-agent: WIPE intent received and spooled; \
                 destructive execution is handled by the root executor, \
                 NOT this agent build"
            );
        }
        other => eprintln!("sextant-agent: unknown intent {other:?}, ignored"),
    }
}

/// ACK_FILE is where the root executor records what it actually did.
const ACK_FILE: &str = "/run/sextant-intent/action.ack";

/// executed_ack reads and clears the outcome the root executor recorded for the
/// last spooled intent ("lock"/"wipe"/"wipe-refused"/"wipe-failed"), so the
/// agent forwards what HAPPENED, not merely that it spooled the request.
pub fn executed_ack(root: &Path) -> Option<String> {
    let path = root.join(ACK_FILE.trim_start_matches('/'));
    let ack = fs::read_to_string(&path).ok()?;
    let _ = fs::remove_file(&path);
    let ack = ack.trim().to_string();
    if ack.is_empty() {
        None
    } else {
        Some(ack)
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
        react(&root, "lock");
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
        react(&root, "wipe");
        // Only a spool marker; the agent itself performs no destruction.
        assert!(root
            .join(SPOOL.trim_start_matches('/'))
            .join("wipe.intent")
            .exists());
        let _ = fs::remove_dir_all(&root);
    }

    #[test]
    fn unknown_intent_is_ignored() {
        let root = tmp("unknown");
        react(&root, "explode"); // must not panic or spool anything
        assert!(!root
            .join(SPOOL.trim_start_matches('/'))
            .join("explode.intent")
            .exists());
        let _ = fs::remove_dir_all(&root);
    }

    #[test]
    fn executed_ack_reads_and_clears_the_executor_outcome() {
        let root = tmp("ack");
        // No outcome recorded yet.
        assert_eq!(executed_ack(&root), None);
        // The root executor records what it did.
        let dir = root.join(SPOOL.trim_start_matches('/'));
        fs::create_dir_all(&dir).unwrap();
        fs::write(dir.join("action.ack"), "wipe-refused\n").unwrap();
        assert_eq!(executed_ack(&root).as_deref(), Some("wipe-refused"));
        // Consumed once: a second read is empty.
        assert_eq!(executed_ack(&root), None);
        let _ = fs::remove_dir_all(&root);
    }
}
