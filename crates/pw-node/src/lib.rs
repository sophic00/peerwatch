//! Native Tokio runtime for PeerWatch v3 (`pw-node`).
//!
//! Combines `pw-core` (pure Sans-I/O protocol) with:
//! - [`transport`]: **`str0m`** Sans-I/O WebRTC (ICE/DTLS/SCTP)
//! - [`store`]: RAM hot tier + crash-safe segment pack store
//! - [`media`]: `ffmpeg` remux, HW-accelerated seekable transcode-ahead & WebVTT
//! - [`player`]: `mpv` JSON-RPC (`observe_property`) + localhost range server
//! - [`web_server`]: tokenized `127.0.0.1` server hosting the shared `web/` UI

pub mod media;
pub mod player;
pub mod store;
pub mod transport;
pub mod web_server;

use std::net::SocketAddr;
use std::path::PathBuf;

use pw_core::api::CoreConfig;
use pw_core::types::RoomMode;

/// Native playback presentation strategy (`docs/design/frontends.md §3`).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum PlayerMode {
    /// Serve the shared `web/` UI + verified fMP4 segments to the user's real browser.
    LocalWebUi,
    /// Drive an external `mpv` window over JSON-RPC + localhost HTTP range server.
    Mpv,
    /// Headless seeder / automation daemon with no local video playback.
    Headless,
}

/// Top-level native daemon configuration.
#[derive(Debug, Clone)]
pub struct NodeConfig {
    pub core: CoreConfig,
    pub relay_url: String,
    pub player_mode: PlayerMode,
    pub local_ui_bind: SocketAddr,
    pub cache_dir: PathBuf,
    pub cache_cap_gib: u64,
}

impl NodeConfig {
    pub fn default_with_mode(mode: RoomMode, player_mode: PlayerMode) -> Self {
        let core = CoreConfig {
            default_mode: mode,
            ..Default::default()
        };
        Self {
            core,
            relay_url: "wss://127.0.0.1:7878/pw".to_string(),
            player_mode,
            local_ui_bind: SocketAddr::from(([127, 0, 0, 1], 0)),
            cache_dir: PathBuf::from(".peerwatch-cache"),
            cache_cap_gib: 20,
        }
    }
}
