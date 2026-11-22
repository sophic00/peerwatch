//! `peerwatch` CLI — native daemon, local web UI host, and control client.

use clap::{Parser, Subcommand, ValueEnum};
use pw_core::types::{PROTOCOL_VERSION, RoomMode};
use pw_node::{NodeConfig, PlayerMode};
use tracing_subscriber::EnvFilter;

#[derive(Debug, Parser)]
#[command(name = "peerwatch", version, about = "Serverless P2P watch party (v3)")]
struct Cli {
    #[command(subcommand)]
    command: Commands,
}

#[derive(Debug, Clone, Copy, ValueEnum)]
enum ModeArg {
    Democratic,
    Collaborative,
}

impl From<ModeArg> for RoomMode {
    fn from(value: ModeArg) -> Self {
        match value {
            ModeArg::Democratic => Self::Democratic,
            ModeArg::Collaborative => Self::Collaborative,
        }
    }
}

#[derive(Debug, Clone, Copy, ValueEnum)]
enum PlayerArg {
    Web,
    Mpv,
    Headless,
}

impl From<PlayerArg> for PlayerMode {
    fn from(value: PlayerArg) -> Self {
        match value {
            PlayerArg::Web => Self::LocalWebUi,
            PlayerArg::Mpv => Self::Mpv,
            PlayerArg::Headless => Self::Headless,
        }
    }
}

#[derive(Debug, Subcommand)]
enum Commands {
    /// Start the long-running native PeerWatch daemon.
    Daemon {
        #[arg(long, value_enum, default_value_t = PlayerArg::Web)]
        player: PlayerArg,
        #[arg(long, value_enum, default_value_t = ModeArg::Democratic)]
        default_mode: ModeArg,
        #[arg(long)]
        relay: Option<String>,
    },
    /// Create a new ephemeral room through the running daemon.
    Create {
        #[arg(long, value_enum, default_value_t = ModeArg::Democratic)]
        mode: ModeArg,
        #[arg(long, default_value_t = 3)]
        invites: u32,
    },
    /// Join a room using a `pw3_...` invite token.
    Join { invite: String },
    /// Check local ffmpeg, HW encoders, mpv, STUN/TURN, and relay reachability.
    Doctor,
}

#[tokio::main]
async fn main() {
    tracing_subscriber::fmt()
        .with_env_filter(EnvFilter::from_default_env())
        .init();

    let cli = Cli::parse();
    match cli.command {
        Commands::Daemon {
            player,
            default_mode,
            relay,
        } => {
            let mut cfg = NodeConfig::default_with_mode(default_mode.into(), player.into());
            if let Some(url) = relay {
                cfg.relay_url = url;
            }
            tracing::info!(
                protocol_version = PROTOCOL_VERSION,
                player_mode = ?cfg.player_mode,
                default_mode = ?cfg.core.default_mode,
                relay = %cfg.relay_url,
                "peerwatch daemon scaffold ready (M1 wires str0m + local UI + relay)"
            );
        }
        Commands::Create { mode, invites } => {
            println!("create room (mode={mode:?}, invites={invites}) — daemon IPC arrives in M1");
        }
        Commands::Join { invite } => {
            println!(
                "join room (token len={}) — invite bootstrap arrives in M1",
                invite.len()
            );
        }
        Commands::Doctor => {
            println!("peerwatch v3 doctor (protocol v{PROTOCOL_VERSION})");
            println!(
                "preferred HW encoders: {:?}",
                pw_node::media::preferred_encoders()
            );
        }
    }
}
