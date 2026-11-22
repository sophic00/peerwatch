//! Deterministic multi-peer simulator (`pw-sim`) for `pw-core`.
//!
//! Runs N virtual `PeerCore` instances against a fake monotonic clock and
//! controllable virtual network with zero real sockets, files, or wall clocks.

use std::collections::BTreeMap;

use pw_core::api::{Command, CoreConfig, Event};
use pw_core::types::PeerId;
use pw_core::{Core, PeerCore};

/// Deterministic simulation harness over N virtual peers.
pub struct SimWorld {
    now_ms: u64,
    peers: BTreeMap<PeerId, PeerCore>,
    trace: Vec<(u64, PeerId, usize)>,
}

impl SimWorld {
    pub fn new() -> Self {
        Self {
            now_ms: 0,
            peers: BTreeMap::new(),
            trace: Vec::new(),
        }
    }

    pub fn add_peer(&mut self, peer_id: PeerId, config: CoreConfig) {
        self.peers.insert(peer_id, PeerCore::new(config));
    }

    pub fn now_ms(&self) -> u64 {
        self.now_ms
    }

    pub fn peer(&self, peer_id: &PeerId) -> Option<&PeerCore> {
        self.peers.get(peer_id)
    }

    pub fn peer_mut(&mut self, peer_id: &PeerId) -> Option<&mut PeerCore> {
        self.peers.get_mut(peer_id)
    }

    /// Deliver one event to a specific virtual peer and record command count.
    pub fn deliver(&mut self, peer_id: PeerId, event: Event) -> Vec<Command> {
        let Some(core) = self.peers.get_mut(&peer_id) else {
            return Vec::new();
        };
        let cmds = core.handle(event);
        self.trace.push((self.now_ms, peer_id, cmds.len()));
        cmds
    }

    /// Advance the virtual monotonic clock and tick every peer deterministically.
    pub fn advance_ms(&mut self, delta_ms: u64) {
        self.now_ms = self.now_ms.saturating_add(delta_ms);
        let ids: Vec<PeerId> = self.peers.keys().copied().collect();
        for id in ids {
            self.deliver(
                id,
                Event::Tick {
                    now_ms: self.now_ms,
                },
            );
        }
    }

    pub fn trace(&self) -> &[(u64, PeerId, usize)] {
        &self.trace
    }
}

impl Default for SimWorld {
    fn default() -> Self {
        Self::new()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn deterministic_sim_produces_identical_traces() {
        let run = || {
            let mut world = SimWorld::new();
            let a = PeerId::from_pubkey(&[1u8; 32]);
            let b = PeerId::from_pubkey(&[2u8; 32]);
            world.add_peer(a, CoreConfig::default());
            world.add_peer(b, CoreConfig::default());

            world.advance_ms(100);
            world.deliver(
                a,
                Event::PeerConnected {
                    peer: b,
                    dtls_fingerprint: [0xAA; 32],
                },
            );
            world.advance_ms(100);
            assert_eq!(world.peer(&a).unwrap().connected_peer_count(), 1);
            assert_eq!(world.peer(&a).unwrap().now_ms(), 200);
            world.trace().to_vec()
        };

        assert_eq!(run(), run());
    }
}
