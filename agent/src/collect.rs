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

/// revision reads the deployed revision from the current-system symlink.
pub fn revision() -> String {
    match fs::read_link(CURRENT_SYSTEM) {
        Ok(target) => revision_from_target(&target.to_string_lossy()),
        Err(_) => String::new(), // not NixOS (dev host); report empty
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
