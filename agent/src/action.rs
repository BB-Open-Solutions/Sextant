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
use std::os::unix::fs::PermissionsExt;
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
        "reboot" => {
            // Operator-triggered one-shot reboot (provisioning wizard: reach the
            // BIOS for a Secure Boot / TPM2 firmware step). Non-destructive; the
            // root executor performs the reboot, the agent only records the request.
            spool(root, "reboot");
        }
        "diagnostics" => {
            // Bounded diagnostics collection (design 0010): the root executor
            // gathers a fixed set (journal tail, failed units), never
            // arbitrary commands; the agent uploads the resulting bundle.
            spool(root, "diagnostics");
        }
        "provision" => {
            // Provisioning-ceremony step (wizard): the root executor advances
            // whatever is possible right now - enrol Secure Boot platform keys
            // when the firmware is in setup mode, seal the LUKS keyslot to the
            // TPM2 once Secure Boot enforces - and acks the milestone. Both
            // operations are constructive and idempotent-guarded in the
            // executor; the agent only records the request.
            spool(root, "provision");
        }
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

/// ACK_FILE is where the root executor records what it actually did. It
/// lives in persistent state, NOT the tmpfs spool: the provisioning steps
/// (Secure Boot enrol, TPM2 seal) reboot immediately after recording their
/// outcome, and the ack must survive that reboot to reach the console.
const ACK_FILE: &str = "/var/lib/sextant-agent/action.ack";

/// RECOVERY_FILE is where the root executor leaves the LUKS recovery key it
/// minted during the provisioning ceremony (design 0009), for the agent to
/// escrow to the console. One-shot: the agent deletes it the moment the
/// server confirms sealing. Confidentiality rests on the 0700 owner-only
/// state directory - only this agent user and root can reach the file, and
/// it exists only for the enroll-to-confirm window.
const RECOVERY_FILE: &str = "/var/lib/sextant-agent/recovery.key";

/// DIAG_FILE is where the root executor leaves the collected diagnostics
/// bundle (gzip, design 0010) for the agent to upload. One-shot like the
/// recovery key: deleted only after the server confirmed receipt.
const DIAG_FILE: &str = "/var/lib/sextant-agent/diagnostics.gz";

/// pending_diagnostics returns the bundle awaiting upload, if any.
pub fn pending_diagnostics(root: &Path) -> Option<Vec<u8>> {
    let bundle = fs::read(root.join(DIAG_FILE.trim_start_matches('/'))).ok()?;
    if bundle.is_empty() {
        None
    } else {
        Some(bundle)
    }
}

/// clear_diagnostics deletes the local bundle after a confirmed upload.
pub fn clear_diagnostics(root: &Path) {
    let _ = fs::remove_file(root.join(DIAG_FILE.trim_start_matches('/')));
}

/// pending_recovery_key returns the recovery key awaiting escrow, if any.
pub fn pending_recovery_key(root: &Path) -> Option<String> {
    let key = fs::read_to_string(root.join(RECOVERY_FILE.trim_start_matches('/'))).ok()?;
    let key = key.trim().to_string();
    if key.is_empty() {
        None
    } else {
        Some(key)
    }
}

/// clear_recovery_key deletes the local copy - called only after the server
/// confirmed it sealed the key (the X-Recovery-Key-Stored response header),
/// so recovery material is never lost between mint and escrow.
pub fn clear_recovery_key(root: &Path) {
    let _ = fs::remove_file(root.join(RECOVERY_FILE.trim_start_matches('/')));
}

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
        // The lock flag feeds the root executor's wipe interlock; keep its
        // directory owner-only so only this agent user (and root, which ignores
        // the mode) can set it - no third-party can forge the interlock.
        tighten(parent);
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
    // Owner-only: the spool is the privilege boundary (the root executor acts on
    // whatever appears here). Ownership already limits writers to this agent user
    // and root; 0700 also stops any third-party from reading which action is
    // pending, and removes the reliance on the process umask for that guarantee.
    tighten(&dir);
    if let Err(e) = fs::write(dir.join(format!("{intent}.intent")), b"") {
        eprintln!("sextant-agent: could not spool {intent}: {e}");
    }
}

/// tighten best-effort restricts a directory to owner-only (0700). Failure is
/// non-fatal - the directory ownership is the primary control; this only drops
/// the read/traverse bits the default umask would otherwise grant.
fn tighten(dir: &Path) {
    let _ = fs::set_permissions(dir, fs::Permissions::from_mode(0o700));
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
    fn reboot_spools_for_the_executor() {
        let root = tmp("reboot");
        react(&root, "reboot");
        assert!(root
            .join(SPOOL.trim_start_matches('/'))
            .join("reboot.intent")
            .exists());
        let _ = fs::remove_dir_all(&root);
    }

    #[test]
    fn spool_dir_is_owner_only() {
        let root = tmp("perms");
        react(&root, "lock");
        let dir = root.join(SPOOL.trim_start_matches('/'));
        let mode = fs::metadata(&dir).unwrap().permissions().mode() & 0o777;
        assert_eq!(mode, 0o700, "spool dir must be owner-only, got {mode:o}");
        let _ = fs::remove_dir_all(&root);
    }

    #[test]
    fn provision_spools_for_the_executor() {
        let root = tmp("provision");
        react(&root, "provision");
        assert!(root
            .join(SPOOL.trim_start_matches('/'))
            .join("provision.intent")
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
    fn recovery_key_pending_and_clear_lifecycle() {
        let root = tmp("recovery");
        assert_eq!(pending_recovery_key(&root), None);
        let f = root.join(RECOVERY_FILE.trim_start_matches('/'));
        fs::create_dir_all(f.parent().unwrap()).unwrap();
        fs::write(&f, "modhex-key\n").unwrap();
        // Reads do NOT consume: the key stays pending until the server
        // confirms sealing (unlike executed_ack's read-and-clear).
        assert_eq!(pending_recovery_key(&root).as_deref(), Some("modhex-key"));
        assert_eq!(pending_recovery_key(&root).as_deref(), Some("modhex-key"));
        clear_recovery_key(&root);
        assert_eq!(pending_recovery_key(&root), None);
        let _ = fs::remove_dir_all(&root);
    }

    #[test]
    fn executed_ack_reads_and_clears_the_executor_outcome() {
        let root = tmp("ack");
        // No outcome recorded yet.
        assert_eq!(executed_ack(&root), None);
        // The root executor records what it did (persistent state, so an
        // ack written right before a provisioning reboot survives it).
        let ack = root.join(ACK_FILE.trim_start_matches('/'));
        fs::create_dir_all(ack.parent().unwrap()).unwrap();
        fs::write(&ack, "wipe-refused\n").unwrap();
        assert_eq!(executed_ack(&root).as_deref(), Some("wipe-refused"));
        // Consumed once: a second read is empty.
        assert_eq!(executed_ack(&root), None);
        let _ = fs::remove_dir_all(&root);
    }
}
