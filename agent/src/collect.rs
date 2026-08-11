//! Local observations: the deployed system revision and (optionally) the
//! nixos-facter hardware document. Pure string handling is split out so it
//! is unit-testable without a NixOS host.

use serde::Serialize;
use std::fs;
use std::path::Path;
use std::process::Command;
use std::thread;
use std::time::Duration;

/// Usage is the device's live resource utilisation for a beat. Field names match
/// the console's wire contract (observed.Usage, camelCase). Any probe that fails
/// leaves its fields zero; an all-zero Usage reads as "not reported" console-side.
#[derive(Serialize, Default, PartialEq, Debug)]
pub struct Usage {
    #[serde(rename = "cpuPct")]
    pub cpu_pct: u8,
    #[serde(rename = "memUsedMB")]
    pub mem_used_mb: u32,
    #[serde(rename = "memTotalMB")]
    pub mem_total_mb: u32,
    #[serde(rename = "diskUsedGB")]
    pub disk_used_gb: u32,
    #[serde(rename = "diskTotalGB")]
    pub disk_total_gb: u32,
}

/// collect_usage samples CPU (a short /proc/stat delta), memory (/proc/meminfo)
/// and the root filesystem (df). Best-effort: a failed probe leaves zeros.
pub fn collect_usage() -> Usage {
    let mut u = Usage::default();
    if let Some((used, total)) = fs::read_to_string("/proc/meminfo")
        .ok()
        .and_then(|s| parse_meminfo(&s))
    {
        u.mem_used_mb = used;
        u.mem_total_mb = total;
    }
    if let Some((used, total)) = read_disk("/") {
        u.disk_used_gb = used;
        u.disk_total_gb = total;
    }
    u.cpu_pct = sample_cpu();
    u
}

/// cpu_times reads the aggregate (idle, total) jiffies from /proc/stat's first line.
fn cpu_times() -> Option<(u64, u64)> {
    let s = fs::read_to_string("/proc/stat").ok()?;
    let line = s.lines().next()?; // "cpu  user nice system idle iowait ..."
    let nums: Vec<u64> = line
        .split_whitespace()
        .skip(1)
        .filter_map(|x| x.parse().ok())
        .collect();
    if nums.len() < 4 {
        return None;
    }
    let idle = nums[3] + nums.get(4).copied().unwrap_or(0); // idle + iowait
    let total: u64 = nums.iter().sum();
    Some((idle, total))
}

/// sample_cpu returns busy CPU percent over a short window (0 if unavailable).
fn sample_cpu() -> u8 {
    let (i1, t1) = match cpu_times() {
        Some(v) => v,
        None => return 0,
    };
    thread::sleep(Duration::from_millis(200));
    let (i2, t2) = match cpu_times() {
        Some(v) => v,
        None => return 0,
    };
    let dt = t2.saturating_sub(t1);
    if dt == 0 {
        return 0;
    }
    let busy = dt.saturating_sub(i2.saturating_sub(i1));
    (busy.saturating_mul(100) / dt).min(100) as u8
}

/// parse_meminfo returns (used_mb, total_mb) from /proc/meminfo contents.
fn parse_meminfo(s: &str) -> Option<(u32, u32)> {
    let field = |name: &str| -> u64 {
        s.lines()
            .find_map(|l| l.strip_prefix(name))
            .and_then(|v| v.split_whitespace().next())
            .and_then(|n| n.parse().ok())
            .unwrap_or(0)
    };
    let total_kb = field("MemTotal:");
    if total_kb == 0 {
        return None;
    }
    let avail_kb = field("MemAvailable:");
    let used_kb = total_kb.saturating_sub(avail_kb);
    Some(((used_kb / 1024) as u32, (total_kb / 1024) as u32))
}

/// read_disk returns (used_gb, total_gb) for a mount point via `df -P -k`.
fn read_disk(path: &str) -> Option<(u32, u32)> {
    let out = Command::new("df").args(["-P", "-k", path]).output().ok()?;
    if !out.status.success() {
        return None;
    }
    parse_df(&String::from_utf8_lossy(&out.stdout))
}

/// parse_df reads the data row of POSIX `df -P -k` output: total and used in KB.
fn parse_df(s: &str) -> Option<(u32, u32)> {
    let cols: Vec<&str> = s.lines().nth(1)?.split_whitespace().collect();
    if cols.len() < 3 {
        return None;
    }
    let total_kb: u64 = cols[1].parse().ok()?;
    let used_kb: u64 = cols[2].parse().ok()?;
    let gb = |kb: u64| (kb / 1024 / 1024) as u32;
    Some((gb(used_kb), gb(total_kb)))
}

/// CURRENT_SYSTEM is the running system's store symlink on NixOS.
pub const CURRENT_SYSTEM: &str = "/run/current-system";

/// revision reports the deployed revision. It prefers the git revision the
/// NixOS agent module publishes (SEXTANT_REVISION_FILE, sourced from
/// system.configurationRevision) - that is what a rollout target is compared
/// against - and falls back to the store label when the file is absent or
/// empty (the overlay did not set configurationRevision).
pub fn revision() -> String {
    let from_file = std::env::var("SEXTANT_REVISION_FILE")
        .ok()
        .and_then(|p| fs::read_to_string(p).ok());
    let symlink = fs::read_link(CURRENT_SYSTEM)
        .ok()
        .map(|t| t.to_string_lossy().into_owned());
    revision_of(from_file.as_deref(), symlink.as_deref())
}

/// revision_of is the pure selection: the published git revision if present
/// and non-empty, else the store-label derived from the current-system
/// symlink, else empty (not NixOS). Split out so it is testable without a
/// filesystem.
pub fn revision_of(rev_file: Option<&str>, symlink_target: Option<&str>) -> String {
    if let Some(rev) = rev_file {
        let rev = rev.trim();
        if !rev.is_empty() {
            return rev.chars().take(64).collect();
        }
    }
    match symlink_target {
        Some(target) => revision_from_target(target),
        None => String::new(),
    }
}

/// revision_from_target derives a compact revision identifier from a store
/// path like /nix/store/<hash>-nixos-system-<host>-<version>.
pub fn revision_from_target(target: &str) -> String {
    let base = Path::new(target)
        .file_name()
        .map(|s| s.to_string_lossy().into_owned())
        .unwrap_or_default();
    // Drop the store hash prefix (everything up to and including the first
    // '-'), then the nixos-system- marker; keep what identifies the build.
    let after_hash = match base.split_once('-') {
        Some((_, rest)) => rest,
        None => base.as_str(),
    };
    let rev = after_hash
        .strip_prefix("nixos-system-")
        .unwrap_or(after_hash);
    rev.chars().take(64).collect()
}

/// facts runs nixos-facter and returns its JSON document. A missing or
/// failing facter is not an error for the beat - facts are best-effort.
pub fn facts(facter: &str) -> Option<serde_json::Value> {
    if facter.is_empty() {
        return None;
    }
    let out = Command::new(facter).output().ok()?;
    if !out.status.success() {
        return None;
    }
    serde_json::from_slice(&out.stdout).ok()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn revision_strips_hash_and_marker() {
        assert_eq!(
            revision_from_target("/nix/store/abc123xyz-nixos-system-lt-42-25.11.20260710"),
            "lt-42-25.11.20260710"
        );
        // Non-system store path: keep the informative tail.
        assert_eq!(
            revision_from_target("/nix/store/abc123-something-else"),
            "something-else"
        );
        // Degenerate inputs stay safe.
        assert_eq!(revision_from_target(""), "");
        assert_eq!(revision_from_target("plain"), "plain");
        // Length is bounded for the wire.
        let long = format!("/nix/store/h-nixos-system-{}", "x".repeat(200));
        assert_eq!(revision_from_target(&long).len(), 64);
    }

    #[test]
    fn revision_prefers_published_git_rev() {
        let sym = Some("/nix/store/abc-nixos-system-lt-42-25.11");
        // Published git revision wins over the store label.
        assert_eq!(revision_of(Some("946acf487633\n"), sym), "946acf487633");
        // Empty/whitespace file falls back to the store label.
        assert_eq!(revision_of(Some("  \n"), sym), "lt-42-25.11");
        assert_eq!(revision_of(None, sym), "lt-42-25.11");
        // Neither available (not NixOS): empty.
        assert_eq!(revision_of(None, None), "");
        // The published rev is length-bounded too.
        assert_eq!(revision_of(Some(&"a".repeat(200)), None).len(), 64);
    }

    #[test]
    fn parse_meminfo_computes_used() {
        let s = "MemTotal:       16384000 kB\nMemFree:  2000000 kB\nMemAvailable:    8192000 kB\n";
        // total 16,384,000 kB = 16000 MB; used = (16,384,000 - 8,192,000)/1024 = 8000 MB.
        assert_eq!(parse_meminfo(s), Some((8000, 16000)));
        // No MemTotal -> None.
        assert_eq!(parse_meminfo("Foo: 1 kB\n"), None);
    }

    #[test]
    fn parse_df_reads_data_row() {
        let s = "Filesystem     1024-blocks      Used Available Capacity Mounted on\n\
                 /dev/nvme0n1p2   500107608 104857600 369819648      23% /\n";
        // total 500,107,608 KB -> 476 GB; used 104,857,600 KB -> 100 GB.
        assert_eq!(parse_df(s), Some((100, 476)));
        // Header only -> None.
        assert_eq!(parse_df("Filesystem 1024-blocks Used\n"), None);
    }
}

/// Health is what systemd thinks of this machine: its overall state and the
/// units that failed.
///
/// WHY THIS IS REPORTED. Found on e2e5, 2026-08-04: an activation can fail
/// while /etc has already been switched, so the revision marker names the
/// configuration that was ATTEMPTED. The console compared that to the target,
/// found them equal, and called the device on spec - with directory login,
/// endpoint security and secret delivery all dead on the machine.
///
/// A revision says what a device meant to run. This says whether it works.
/// The console needs both, and needs the second to be able to veto the first.
#[derive(Serialize, Default, Debug, PartialEq)]
pub struct Health {
    /// state is systemd's own verdict: "running", "degraded", "starting",
    /// "maintenance", "stopping". Empty when it could not be read (not
    /// systemd, or the call failed) - unknown must not read as healthy.
    #[serde(skip_serializing_if = "String::is_empty")]
    pub state: String,
    /// failed names the units in a failed state, sorted, capped. The names are
    /// the actionable part: "sssd.service" tells an operator what to look at,
    /// where "degraded" only says something is wrong.
    #[serde(rename = "failedUnits", skip_serializing_if = "Vec::is_empty")]
    pub failed: Vec<String>,
}

/// maxFailedUnits bounds the report. A machine with more than this is broken
/// in a way the list will not diagnose, and an unbounded list is a check-in
/// body an operator's browser has to render.
const MAX_FAILED_UNITS: usize = 20;

/// collect_health asks systemd. Best-effort: a probe that cannot run leaves
/// the state empty, which the console reads as unknown rather than healthy.
pub fn collect_health() -> Health {
    let mut h = Health::default();
    if let Ok(out) = Command::new("systemctl").arg("is-system-running").output() {
        // Deliberately ignoring the exit status: `is-system-running` exits
        // non-zero precisely when the answer is interesting ("degraded" is
        // exit 1). Reading stdout regardless is the point.
        h.state = String::from_utf8_lossy(&out.stdout).trim().to_string();
    }
    if let Ok(out) = Command::new("systemctl")
        .args([
            "list-units",
            "--state=failed",
            "--no-legend",
            "--plain",
            "--no-pager",
        ])
        .output()
    {
        if out.status.success() {
            h.failed = parse_failed_units(&String::from_utf8_lossy(&out.stdout));
        }
    }
    h
}

/// parse_failed_units takes the unit name from each row of
/// `systemctl list-units --no-legend --plain`. Split out so it is testable
/// without systemd.
pub fn parse_failed_units(s: &str) -> Vec<String> {
    let mut out: Vec<String> = s
        .lines()
        // A status bullet survives --plain on some systemd versions. It has to
        // be stripped BEFORE splitting: taking the first token and then
        // discarding it drops the unit name with it, which is what the first
        // version of this did.
        .map(|l| l.trim_start_matches(['\u{25CF}', '*', ' ', '\t']))
        .filter_map(|l| l.split_whitespace().next())
        .filter(|n| n.contains('.'))
        .map(str::to_string)
        .collect();
    out.sort();
    out.dedup();
    out.truncate(MAX_FAILED_UNITS);
    out
}

#[cfg(test)]
mod health_tests {
    use super::*;

    #[test]
    fn parses_unit_names_and_ignores_decoration() {
        // Real shape of `systemctl list-units --state=failed --no-legend
        // --plain`, with the bullet some versions still emit.
        let out = "\
● sssd.service            loaded failed failed System Security Services Daemon
  wazuh-agent.service     loaded failed failed Wazuh endpoint security agent
  openbao-secrets.service loaded failed failed Fetch device secrets from OpenBao
";
        assert_eq!(
            parse_failed_units(out),
            vec![
                "openbao-secrets.service",
                "sssd.service",
                "wazuh-agent.service"
            ]
        );
    }

    #[test]
    fn empty_output_is_no_failures() {
        assert!(parse_failed_units("").is_empty());
        assert!(parse_failed_units("\n  \n").is_empty());
    }

    #[test]
    fn a_healthy_machine_serialises_to_nothing() {
        // An older console must not see new keys on every beat from a machine
        // with nothing wrong: both fields skip when empty.
        let h = Health::default();
        assert_eq!(serde_json::to_string(&h).unwrap(), "{}");
    }
}

/// The last thing comin tried to do, and whether it worked.
///
/// WHY THIS EXISTS. `observed.CheckIn.Error` is documented server-side as
/// carrying "a self-reported failure (e.g. a comin deploy error)", the
/// console renders it, and the acceptance plan expects an incident from it
/// (A9.7). The agent never filled it in, so a device whose deployment failed
/// reported exactly what a healthy one reports and simply kept an older
/// revision - indistinguishable from a device still converging.
///
/// Found on 2026-08-10 when a laptop failed a core update and the console
/// showed a device that was fine. Everything downstream was already built.
///
/// comin keeps its own history in `store.json`, world-readable, newest
/// first: each deployment carries a `status` and an `error_msg`. This reads
/// the newest and reports a failure only when comin says so.
///
/// SILENT ON DOUBT. A missing, unreadable or unparseable store means we do
/// not know, and inventing a failure is worse than reporting none: it would
/// raise an incident for every device that does not run comin at all.
pub fn comin_failure(store_path: &str) -> Option<String> {
    let raw = std::fs::read_to_string(store_path).ok()?;
    let doc: serde_json::Value = serde_json::from_str(&raw).ok()?;
    let last = doc.get("deployments")?.as_array()?.first()?;

    let status = last.get("status").and_then(|v| v.as_str()).unwrap_or("");
    let msg = last.get("error_msg").and_then(|v| v.as_str()).unwrap_or("");

    // "done" is success. An empty status means comin has not finished a
    // deployment yet, which is not a failure either.
    if status.is_empty() || status == "done" {
        return None;
    }
    let branch = last
        .get("generation")
        .and_then(|g| g.get("selected_branch_name"))
        .and_then(|v| v.as_str())
        .unwrap_or("");

    // The operator reads this in a list, so lead with what failed rather
    // than with comin's vocabulary, and keep it to one line.
    let detail = if msg.is_empty() { status } else { msg };
    let text = if branch.is_empty() {
        format!("comin {status}: {detail}")
    } else {
        format!("comin {status} on {branch}: {detail}")
    };
    // The column is not unbounded and a nix error can be a screenful.
    Some(text.chars().take(480).collect())
}

/// What comin's own exporter says is wrong, if anything.
///
/// WHY THIS EXISTS, and why it is not the store. `comin_failure` above reads
/// the deployment history, which only records deployments comin actually
/// STARTED. A configuration that does not evaluate never reaches one, so it
/// leaves no entry at all - and that is the common case, because it is what
/// a bad change looks like when the gate did not catch it.
///
/// Measured on 2026-08-11 during acceptance row A4.5: a station failed to
/// evaluate every two minutes for half an hour while the console showed it
/// with no error, because the history's newest entry was an old success.
///
/// The exporter separates four failures and this reports the first that is
/// set, in the order they happen: a fetch that fails explains an evaluation
/// that never ran.
pub fn comin_metric_failure(body: &str) -> Option<String> {
    // Prometheus text: `name{labels} value`. Only the four booleans matter.
    let flag = |needle: &str| -> bool {
        body.lines().any(|l| {
            let l = l.trim();
            if !l.starts_with(needle) {
                return false;
            }
            // Guard against a prefix match on a longer metric name.
            let rest = &l[needle.len()..];
            if !(rest.starts_with(' ') || rest.starts_with('{')) {
                return false;
            }
            rest.rsplit(' ')
                .next()
                .map(|v| v.trim() != "0")
                .unwrap_or(false)
        })
    };
    // STICKY, and the wording admits it. Measured 2026-08-11: half an hour
    // after a broken ring was reverted and the station had deployed cleanly,
    // comin_last_eval_failed was still 1. The flag records the last
    // evaluation that RAN, and a poll with nothing new to evaluate does not
    // clear it. So these say "last", not "is": a device on target with a
    // stale flag is not broken. The console holds the target revision and can
    // tell the difference; the agent does not, and must not pretend to.
    //
    // Ordered by when they occur: the earliest failure explains the rest.
    for (metric, said) in [
        (
            "comin_last_fetch_failed",
            "last fetch failed: cannot reach the forge",
        ),
        (
            "comin_last_eval_failed",
            "last evaluation failed: the configuration does not evaluate",
        ),
        (
            "comin_last_build_failed",
            "last build failed: the configuration does not build",
        ),
        ("comin_last_deployment_failed", "last deployment failed"),
    ] {
        if flag(metric) {
            return Some(format!("comin: {said}"));
        }
    }
    None
}

#[cfg(test)]
mod comin_tests {
    use super::comin_failure;
    use std::sync::atomic::{AtomicU32, Ordering};

    /// A throwaway store file. std only: the agent ships on every device in
    /// the fleet and a test helper is not worth a dependency in that closure.
    struct Store(std::path::PathBuf);
    impl Store {
        fn new(body: &str) -> Self {
            static N: AtomicU32 = AtomicU32::new(0);
            let p = std::env::temp_dir().join(format!(
                "comin-store-{}-{}.json",
                std::process::id(),
                N.fetch_add(1, Ordering::Relaxed)
            ));
            std::fs::write(&p, body).unwrap();
            Store(p)
        }
        fn path(&self) -> &str {
            self.0.to_str().unwrap()
        }
    }
    impl Drop for Store {
        fn drop(&mut self) {
            let _ = std::fs::remove_file(&self.0);
        }
    }
    fn store(body: &str) -> Store {
        Store::new(body)
    }

    /// The shape comin actually writes, taken from a live station on
    /// 2026-08-10 rather than from the documentation.
    fn deployment(status: &str, err: &str) -> String {
        format!(
            r#"{{"deployments":[{{"uuid":"u1","status":"{status}","error_msg":"{err}",
               "generation":{{"selected_branch_name":"rings/bb-laptops"}}}}]}}"#
        )
    }

    #[test]
    fn a_finished_deployment_is_not_a_failure() {
        let f = store(&deployment("done", ""));
        assert_eq!(comin_failure(f.path()), None);
    }

    #[test]
    fn a_failure_carries_its_message_and_its_branch() {
        let f = store(&deployment("failed", "error: builder for openssl failed"));
        let got = comin_failure(f.path()).expect("a failure must be reported");
        assert!(got.contains("openssl"), "the reason is the point: {got}");
        assert!(
            got.contains("rings/bb-laptops"),
            "which ring failed matters: {got}"
        );
    }

    /// A status other than done with no message is still a failure. Reporting
    /// nothing because comin was terse is how the silence this fixes began.
    #[test]
    fn a_failure_without_a_message_is_still_reported() {
        let f = store(&deployment("failed", ""));
        assert!(comin_failure(f.path()).is_some());
    }

    /// Not knowing is not the same as being fine, but inventing a failure is
    /// worse: it would raise an incident for every device without comin.
    #[test]
    fn silence_when_there_is_nothing_to_read() {
        assert_eq!(comin_failure("/nonexistent/store.json"), None);
        let f = store("this is not json");
        assert_eq!(comin_failure(f.path()), None);
        let f = store(r#"{"deployments":[]}"#);
        assert_eq!(comin_failure(f.path()), None);
        let f = store(&deployment("", ""));
        assert_eq!(comin_failure(f.path()), None);
    }

    /// A nix error is a screenful and the column is not unbounded.
    #[test]
    fn a_long_error_is_cut_rather_than_sent_whole() {
        let long = "x".repeat(4000);
        let f = store(&deployment("failed", &long));
        let got = comin_failure(f.path()).unwrap();
        assert!(
            got.chars().count() <= 480,
            "got {} chars",
            got.chars().count()
        );
    }
}

#[cfg(test)]
mod comin_metric_tests {
    use super::comin_metric_failure;

    /// Captured from a live station on 2026-08-11 while acceptance row A4.5
    /// had it failing to evaluate. Not written by hand: the shape of a real
    /// exporter is the thing worth testing against, and this state cannot be
    /// reproduced once the ring is reverted.
    const FAILING: &str = include_str!("../tests/fixtures/comin-metrics-eval-failed.txt");
    // Same station, half an hour later: the ring reverted, the deployment done,
    // and the eval flag still 1. Captured rather than hand-edited, because the
    // point of it is that this is what comin really reports.
    const STALE: &str = include_str!("../tests/fixtures/comin-metrics-stale-flag.txt");

    #[test]
    fn a_stale_eval_flag_is_reported_as_the_last_evaluation_not_the_current_state() {
        // Deliberately NOT asserting None. The agent cannot tell a stale flag
        // from a live one - it has no target revision to compare against - so
        // suppressing it here would be guessing. What it owes the reader is
        // wording that does not overclaim.
        let got = comin_metric_failure(STALE).expect("the flag is set, so it is reported");
        assert!(got.contains("last evaluation failed"), "got {got}");
        assert!(
            !got.contains("is failing") && !got.contains("currently"),
            "must not claim the failure is happening now: {got}"
        );
        // The deployment in this same reading succeeded, which is exactly why
        // deployment status cannot be used to decide the flag is stale.
        assert!(STALE.contains(r#"status="done""#));
    }

    #[test]
    fn the_real_failing_exporter_is_recognised() {
        let got = comin_metric_failure(FAILING).expect("a live failure must be reported");
        assert!(got.contains("does not evaluate"), "got {got}");
    }

    #[test]
    fn a_healthy_exporter_says_nothing() {
        let healthy = FAILING.replace("comin_last_eval_failed 1", "comin_last_eval_failed 0");
        assert_eq!(comin_metric_failure(&healthy), None);
    }

    /// The earliest failure explains the later ones: a device that cannot
    /// reach the forge has nothing to evaluate, and reporting the evaluation
    /// sends somebody to read a nix error about a fetch problem.
    #[test]
    fn the_earliest_failure_wins() {
        let both = FAILING.replace(
            "comin_last_fetch_failed{remote_name=\"dawo-image\"} 0",
            "comin_last_fetch_failed{remote_name=\"dawo-image\"} 1",
        );
        let got = comin_metric_failure(&both).unwrap();
        assert!(got.contains("reach the forge"), "got {got}");
    }

    /// A metric whose name merely starts with another must not match it.
    #[test]
    fn a_longer_name_is_not_a_prefix_match() {
        let s = "comin_last_eval_failed_total 7\ncomin_last_eval_failed 0\n";
        assert_eq!(comin_metric_failure(s), None);
    }

    #[test]
    fn nothing_to_read_is_not_a_failure() {
        assert_eq!(comin_metric_failure(""), None);
        assert_eq!(comin_metric_failure("garbage"), None);
    }
}

/// Combine what the two sources know into one line for the console.
///
/// Extracted from the check-in loop so it can be tested. Wiring that only
/// exists inside a loop is wiring nobody checks: reverting this to "read the
/// store only" - the state that hid a failing station for half an hour on
/// 2026-08-11 - compiled cleanly and passed every test until this function
/// existed.
///
/// The metric names WHICH stage failed; the store carries the message from
/// the last deployment it managed to record. Where both speak, both are
/// reported, because "the configuration does not evaluate" is the diagnosis
/// and the nix error is the evidence.
pub fn converge_error(metrics: Option<&str>, store_detail: Option<String>) -> Option<String> {
    match (metrics.and_then(comin_metric_failure), store_detail) {
        (Some(what), Some(detail)) => Some(format!("{what} - {detail}")),
        (Some(what), None) => Some(what),
        (None, detail) => detail,
    }
}

/// comin's exporter, read over loopback. Absent or unreachable is silence,
/// not a failure: a device whose exporter is off is not a device in trouble.
pub fn comin_metrics(url: &str) -> Option<String> {
    ureq::get(url)
        .timeout(std::time::Duration::from_secs(3))
        .call()
        .ok()?
        .into_string()
        .ok()
}

#[cfg(test)]
mod converge_error_tests {
    use super::converge_error;

    const EVAL_FAILED: &str = "comin_last_eval_failed 1\ncomin_last_fetch_failed 0\n";
    const HEALTHY: &str = "comin_last_eval_failed 0\ncomin_last_fetch_failed 0\n";

    /// The case that was invisible: comin never started a deployment, so the
    /// store has nothing to say, and only the metric knows.
    #[test]
    fn a_metric_failure_is_reported_without_the_store() {
        let got = converge_error(Some(EVAL_FAILED), None).expect("must report");
        assert!(got.contains("does not evaluate"), "got {got}");
    }

    #[test]
    fn both_sources_are_combined_diagnosis_then_evidence() {
        let got = converge_error(Some(EVAL_FAILED), Some("nix: boom".into())).unwrap();
        assert!(
            got.contains("does not evaluate") && got.contains("boom"),
            "got {got}"
        );
    }

    /// A deployment failure the exporter does not flag still reaches the
    /// console through the store, which is what the first version did.
    #[test]
    fn the_store_still_speaks_when_the_metrics_are_quiet() {
        assert_eq!(
            converge_error(Some(HEALTHY), Some("comin failed: boom".into())).as_deref(),
            Some("comin failed: boom")
        );
    }

    #[test]
    fn silence_when_neither_has_anything() {
        assert_eq!(converge_error(Some(HEALTHY), None), None);
        assert_eq!(converge_error(None, None), None);
    }
}
