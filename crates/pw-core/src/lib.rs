//! Pure Sans-I/O protocol core for PeerWatch v3.
//!
//! `pw-core` performs zero I/O, reads no system clocks, and uses no ambient
//! randomness. Native daemons (`pw-node`), browser Web Workers (`pw-wasm`),
//! and the deterministic simulator (`pw-sim`) all drive [`PeerCore`] through
//! the same [`Event`] / [`Command`] loop.

pub mod api;
pub mod bitmap;
pub mod errors;
pub mod merkle;
pub mod room;
pub mod sync;
pub mod types;

use std::collections::BTreeMap;

use api::{Command, CoreConfig, Event};
use room::RoomState;
use sync::SyncController;
use types::{PeerId, RoomMode};

/// Deterministic Sans-I/O protocol engine contract (`docs/spec/core-api.md`).
pub trait Core {
    fn handle(&mut self, event: Event) -> Vec<Command>;
    fn next_timeout(&self) -> Option<u64>;
}

/// Top-level PeerWatch v3 state machine (M0 scaffold).
#[derive(Debug)]
pub struct PeerCore {
    config: CoreConfig,
    now_ms: u64,
    room: Option<RoomState>,
    sync: SyncController,
    timers: BTreeMap<u32, u64>,
    connected_peers: BTreeMap<PeerId, [u8; 32]>,
}

impl PeerCore {
    pub fn new(config: CoreConfig) -> Self {
        let sync = SyncController::new(config.sync);
        Self {
            config,
            now_ms: 0,
            room: None,
            sync,
            timers: BTreeMap::new(),
            connected_peers: BTreeMap::new(),
        }
    }

    pub fn now_ms(&self) -> u64 {
        self.now_ms
    }

    pub fn config(&self) -> &CoreConfig {
        &self.config
    }

    pub fn room(&self) -> Option<&RoomState> {
        self.room.as_ref()
    }

    pub fn sync_controller(&self) -> &SyncController {
        &self.sync
    }

    pub fn connected_peer_count(&self) -> usize {
        self.connected_peers.len()
    }

    pub fn init_room_mode(&mut self, mode: RoomMode) {
        self.room = Some(RoomState::new_empty(mode));
    }
}

impl Core for PeerCore {
    fn handle(&mut self, event: Event) -> Vec<Command> {
        match event {
            Event::Start {
                config,
                identity: _,
                now_ms,
            } => {
                self.config = config;
                self.now_ms = now_ms;
                self.sync = SyncController::new(self.config.sync);
                Vec::new()
            }
            Event::Tick { now_ms } => {
                if now_ms >= self.now_ms {
                    self.now_ms = now_ms;
                }
                Vec::new()
            }
            Event::Timer { id, now_ms } => {
                if now_ms >= self.now_ms {
                    self.now_ms = now_ms;
                }
                self.timers.remove(&id.0);
                Vec::new()
            }
            Event::PeerConnected {
                peer,
                dtls_fingerprint,
            } => {
                self.connected_peers.insert(peer, dtls_fingerprint);
                Vec::new()
            }
            Event::PeerDisconnected { peer, .. } => {
                self.connected_peers.remove(&peer);
                Vec::new()
            }
            Event::Shutdown => {
                self.connected_peers.clear();
                self.timers.clear();
                Vec::new()
            }
            _ => Vec::new(),
        }
    }

    fn next_timeout(&self) -> Option<u64> {
        self.timers.values().copied().min()
    }
}
