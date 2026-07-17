//! Security posture probe: Secure Boot and TPM2 LUKS state, read-only.
//! The agent observes; the console renders the gap to the target. All
//! paths are rooted so the derivation logic is testable against fixture
//! trees without a real EFI/TPM host.

use std::path::{Path, PathBuf};

/// Posture is the observed pair reported on each check-in.
#[derive(Debug, Default, PartialEq)]
pub struct Posture {
    /// "" | "off" | "audit" | "enforcing"
    pub sb: &'static str,
    /// "" | "absent" | "present" | "enrolled"
    pub tpm2: &'static str,
}

/// probe reads posture under `root` ("/" in production). Anything it
/// cannot determine stays empty, which the server treats as unknown -
/// the probe never guesses.
pub fn probe(root: &Path) -> Posture {
    Posture {
        sb: secure_boot(root),
        tpm2: tpm2(root),
    }
}

/// secure_boot classifies from the EFI SecureBoot variable plus whether
/// machine-owned keys have been created (sbctl store present):
///   SecureBoot efivar = 1              -> enforcing
///   = 0 and sbctl keys exist           -> audit (keys made, not yet on)
///   = 0 and no keys                    -> off
/// The efivar's value is the 5th byte (4-byte attribute prefix, then the
/// boolean), the kernel's documented layout.
fn secure_boot(root: &Path) -> &'static str {
    let dir = root.join("sys/firmware/efi/efivars");
    let enabled = read_efi_bool(&dir, "SecureBoot");
    match enabled {
        Some(true) => "enforcing",
        Some(false) => {
            if root.join("var/lib/sbctl/keys").exists() {
                "audit"
            } else {
                "off"
            }
        }
        // No EFI SecureBoot variable: legacy/CSM boot, report off.
        None => {
            if root.join("sys/firmware/efi").exists() {
                "off"
            } else {
                "" // not an EFI system at all: unknown, do not instruct
            }
        }
    }
}

/// read_efi_bool finds SecureBoot-<guid> and returns its boolean value.
fn read_efi_bool(dir: &Path, name: &str) -> Option<bool> {
    let entries = std::fs::read_dir(dir).ok()?;
    for e in entries.flatten() {
        let fname = e.file_name();
        let s = fname.to_string_lossy();
        if s.starts_with(&format!("{name}-")) {
            let bytes = std::fs::read(e.path()).ok()?;
            // 4 attribute bytes then the value; tolerate a bare value too.
            return bytes.last().map(|&b| b == 1);
        }
    }
    None
}

/// tpm2 classifies from device presence and the executor's enrolment stamp:
///   no TPM resource manager device      -> absent
///   present and the seal stamp exists    -> enrolled
///   present, no stamp                    -> present
///
/// The stamp (written by sextant-actd after a successful systemd-cryptenroll)
/// replaced an /etc/crypttab check: on NixOS with a systemd initrd that file
/// only exists INSIDE the initrd, never on the running system, so the old
/// probe could neither see a wired tpm2 unlock nor a completed enrolment.
fn tpm2(root: &Path) -> &'static str {
    let present = root.join("dev/tpmrm0").exists() || root.join("sys/class/tpm/tpm0").exists();
    if !present {
        return "absent";
    }
    if root.join("var/lib/sextant-agent/tpm2-enrolled").exists() {
        "enrolled"
    } else {
        "present"
    }
}

/// default_root is "/" in production; a helper so main stays readable.
pub fn default_root() -> PathBuf {
    PathBuf::from("/")
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::fs;

    // A tiny fixture builder over a temp dir. No external crate: manual
    // temp path under the OS temp dir, cleaned by the test.
    struct Fixture {
        root: PathBuf,
    }
    impl Fixture {
        fn new(name: &str) -> Fixture {
            let root = std::env::temp_dir().join(format!("sxt-posture-{name}"));
            let _ = fs::remove_dir_all(&root);
            fs::create_dir_all(&root).unwrap();
            Fixture { root }
        }
        fn write(&self, rel: &str, bytes: &[u8]) {
            let p = self.root.join(rel);
            fs::create_dir_all(p.parent().unwrap()).unwrap();
            fs::write(p, bytes).unwrap();
        }
        fn mkdir(&self, rel: &str) {
            fs::create_dir_all(self.root.join(rel)).unwrap();
        }
    }
    impl Drop for Fixture {
        fn drop(&mut self) {
            let _ = fs::remove_dir_all(&self.root);
        }
    }

    // EFI SecureBoot var: 4 attribute bytes + the value byte.
    fn efivar(on: bool) -> Vec<u8> {
        vec![6, 0, 0, 0, if on { 1 } else { 0 }]
    }

    #[test]
    fn secure_boot_states() {
        let f = Fixture::new("sb-enforcing");
        f.write(
            "sys/firmware/efi/efivars/SecureBoot-8be4df61-guid",
            &efivar(true),
        );
        assert_eq!(secure_boot(&f.root), "enforcing");

        let f = Fixture::new("sb-audit");
        f.write(
            "sys/firmware/efi/efivars/SecureBoot-8be4df61-guid",
            &efivar(false),
        );
        f.mkdir("var/lib/sbctl/keys");
        assert_eq!(secure_boot(&f.root), "audit");

        let f = Fixture::new("sb-off");
        f.write(
            "sys/firmware/efi/efivars/SecureBoot-8be4df61-guid",
            &efivar(false),
        );
        assert_eq!(secure_boot(&f.root), "off");

        // Non-EFI host: unknown, no instruction.
        let f = Fixture::new("sb-legacy");
        assert_eq!(secure_boot(&f.root), "");
    }

    #[test]
    fn tpm2_states() {
        let f = Fixture::new("tpm-absent");
        assert_eq!(tpm2(&f.root), "absent");

        let f = Fixture::new("tpm-present");
        f.write("dev/tpmrm0", b"");
        assert_eq!(tpm2(&f.root), "present");

        let f = Fixture::new("tpm-enrolled");
        f.write("dev/tpmrm0", b"");
        f.write("var/lib/sextant-agent/tpm2-enrolled", b"");
        assert_eq!(tpm2(&f.root), "enrolled");
    }

    #[test]
    fn probe_combines() {
        let f = Fixture::new("combined");
        f.write("sys/firmware/efi/efivars/SecureBoot-guid", &efivar(true));
        f.write("dev/tpmrm0", b"");
        let p = probe(&f.root);
        assert_eq!(p.sb, "enforcing");
        assert_eq!(p.tpm2, "present");
    }
}
