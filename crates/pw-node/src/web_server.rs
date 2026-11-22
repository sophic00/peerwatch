//! Tokenized `127.0.0.1` server hosting the shared `web/` UI and verified
//! fMP4 segment endpoints for the native daemon's default browser MSE mode.

use axum::{Router, routing::get};

/// Build the minimal local HTTP router (expanded in M1 with token auth,
/// `/ws/control`, and `/media/{stream}/{seg}`).
pub fn build_local_ui_router() -> Router {
    Router::new().route("/health", get(|| async { "ok" }))
}
