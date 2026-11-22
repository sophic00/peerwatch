//! Native `mpv` JSON-RPC adapter (`observe_property("time-pos")`) and
//! tokenized `127.0.0.1` HTTP range server (`docs/design/frontends.md §3.2`).

/// Placeholder configuration for the `mpv` player adapter (implemented in M1).
#[derive(Debug, Clone)]
pub struct MpvConfig {
    pub binary: String,
    pub sandbox: String,
}

impl Default for MpvConfig {
    fn default() -> Self {
        Self {
            binary: "mpv".to_string(),
            sandbox: "auto".to_string(),
        }
    }
}
