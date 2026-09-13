//! DSKY — builds customized Windows and Linux OS install media.
//!
//! This crate is a placeholder reserving the name while the tool is being
//! built. There is no functionality here yet.
//!
//! See <https://github.com/uplinkresearch/dsky>.

/// The version of this placeholder, taken from `Cargo.toml`.
pub const VERSION: &str = env!("CARGO_PKG_VERSION");

#[cfg(test)]
mod tests {
    #[test]
    fn version_is_set() {
        assert!(!super::VERSION.is_empty());
    }
}
