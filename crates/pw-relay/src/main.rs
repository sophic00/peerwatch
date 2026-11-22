//! `pw-relay` — 100% stateless in-memory WebSocket signaling mailbox (`docs/spec/protocol.md §11`).
//!
//! Stores zero disk state and zero lobby history. Forwards opaque encrypted
//! `SignalEnvelope` and `InviteEnvelope` frames between online channel aliases.

use std::net::SocketAddr;

use axum::{Router, routing::get};
use clap::Parser;
use pw_core::types::{MAX_RELAY_FRAME, PROTOCOL_VERSION};
use tracing_subscriber::EnvFilter;

#[derive(Debug, Parser)]
#[command(
    name = "pw-relay",
    version,
    about = "Stateless PeerWatch v3 signaling relay"
)]
struct RelayCli {
    #[arg(long, default_value = "127.0.0.1:7878")]
    listen: SocketAddr,
    #[arg(long, default_value_t = 1000)]
    max_channels: usize,
    #[arg(long, default_value_t = 32)]
    max_peers_per_channel: usize,
    #[arg(long, default_value_t = 90)]
    idle_timeout_s: u64,
}

#[tokio::main]
async fn main() {
    tracing_subscriber::fmt()
        .with_env_filter(EnvFilter::from_default_env())
        .init();

    let cli = RelayCli::parse();
    let app = Router::new().route("/health", get(|| async { "ok" }));

    tracing::info!(
        protocol_version = PROTOCOL_VERSION,
        listen = %cli.listen,
        max_frame_bytes = MAX_RELAY_FRAME,
        max_channels = cli.max_channels,
        max_peers_per_channel = cli.max_peers_per_channel,
        idle_timeout_s = cli.idle_timeout_s,
        "pw-relay scaffold configured (WebSocket upgrade arrives in M1)"
    );

    let listener = tokio::net::TcpListener::bind(cli.listen)
        .await
        .expect("bind relay listen address");
    axum::serve(listener, app)
        .await
        .expect("serve relay HTTP/WS");
}
