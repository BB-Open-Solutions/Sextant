//! Local observations: the deployed system revision and (optionally) the
//! nixos-facter hardware document. Pure string handling is split out so it
//! is unit-testable without a NixOS host.

use std::fs;
use std::path::Path;
use std::process::Command;

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
}
