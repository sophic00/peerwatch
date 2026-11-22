//! Self-verifying CRDT room state and dual-mode authority objects
//! (`docs/spec/protocol.md §5`, `docs/design/governance.md`).

use std::collections::{BTreeMap, BTreeSet};

use serde::{Deserialize, Serialize};

use crate::errors::CoreError;
use crate::types::{
    EncPubKeyBytes, Hash32, PeerId, PublicKeyBytes, RoomId, RoomMode, SignatureBytes, StreamId,
    Term,
};

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct RoomGenesis {
    pub room_id: RoomId,
    pub mode: RoomMode,
    pub creator_pubkey: PublicKeyBytes,
    pub created_ms: u64,
    pub signature: Vec<u8>,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct MemberAdd {
    pub room_id: RoomId,
    pub invite_id: [u8; 16],
    pub member_pubkey: PublicKeyBytes,
    pub member_enc_pubkey: EncPubKeyBytes,
    pub accepter_pubkey: PublicKeyBytes,
    pub signature: Vec<u8>,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct PeerSignalKey {
    pub room_id: RoomId,
    pub peer_pubkey: PublicKeyBytes,
    pub signal_pub: EncPubKeyBytes,
    pub seq: u64,
    pub signature: Vec<u8>,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct QueueEntry {
    pub entry_id: Hash32,
    pub stream_id: StreamId,
    pub proposer_pubkey: PublicKeyBytes,
    pub lamport: u64,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub enum HostAuthorityKind {
    VoteCertificate { cert_hash: Hash32, vote_count: u32 },
    TakeoverCertificate { claim_hash: Hash32, ack_count: u32 },
    QueueClaim { entry_id: Hash32 },
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct HostLease {
    pub room_id: RoomId,
    pub term: Term,
    pub prev_lease_hash: Option<Hash32>,
    pub stream_id: StreamId,
    pub host_pubkey: PublicKeyBytes,
    pub started_at_media_ms: u64,
    pub authority: HostAuthorityKind,
    pub lease_hash: Hash32,
    pub signature: Vec<u8>,
}

impl HostLease {
    /// Deterministic lease precedence: higher `term`, then more votes/acks,
    /// then lexicographically smallest `lease_hash`.
    pub fn outranks(&self, other: &Self) -> bool {
        if self.term != other.term {
            return self.term > other.term;
        }
        let rank = |a: &HostAuthorityKind| match *a {
            HostAuthorityKind::VoteCertificate { vote_count, .. } => vote_count,
            HostAuthorityKind::TakeoverCertificate { ack_count, .. } => ack_count,
            HostAuthorityKind::QueueClaim { .. } => 0,
        };
        let r_self = rank(&self.authority);
        let r_other = rank(&other.authority);
        if r_self != r_other {
            return r_self > r_other;
        }
        self.lease_hash < other.lease_hash
    }
}

/// Join-semilattice room state synchronized via `STATE_DIGEST` / `STATE_SYNC`.
#[derive(Debug, Clone)]
pub struct RoomState {
    pub mode: RoomMode,
    pub genesis: Option<RoomGenesis>,
    pub admitted_members: BTreeSet<PublicKeyBytes>,
    pub banned_members: BTreeSet<PublicKeyBytes>,
    pub active_roster: BTreeSet<PeerId>,
    pub signal_keys: BTreeMap<PublicKeyBytes, PeerSignalKey>,
    pub current_lease: Option<HostLease>,
    pub queue_added: BTreeMap<Hash32, QueueEntry>,
    pub queue_removed: BTreeSet<Hash32>,
}

impl RoomState {
    pub fn new_empty(mode: RoomMode) -> Self {
        Self {
            mode,
            genesis: None,
            admitted_members: BTreeSet::new(),
            banned_members: BTreeSet::new(),
            active_roster: BTreeSet::new(),
            signal_keys: BTreeMap::new(),
            current_lease: None,
            queue_added: BTreeMap::new(),
            queue_removed: BTreeSet::new(),
        }
    }

    /// Merge a verified `HostLease` monotonically.
    pub fn merge_lease(&mut self, lease: HostLease) -> bool {
        let should_replace = match &self.current_lease {
            None => true,
            Some(cur) => lease.outranks(cur),
        };
        if should_replace {
            self.current_lease = Some(lease);
        }
        should_replace
    }

    /// Merge a self-signed `PeerSignalKey` (higher `seq` wins).
    pub fn merge_signal_key(&mut self, key: PeerSignalKey) -> bool {
        let replace = self
            .signal_keys
            .get(&key.peer_pubkey)
            .is_none_or(|cur| key.seq > cur.seq);
        if replace {
            self.signal_keys.insert(key.peer_pubkey, key);
        }
        replace
    }

    /// Apply a verified `KickCertificate` (Option B exclusion).
    pub fn apply_kick_ban(&mut self, target_pubkey: PublicKeyBytes) {
        self.banned_members.insert(target_pubkey);
        self.active_roster
            .remove(&PeerId::from_pubkey(&target_pubkey));
    }

    /// Check whether a peer is allowed to authenticate (`AUTH`).
    pub fn can_authenticate(&self, pubkey: &PublicKeyBytes) -> bool {
        self.admitted_members.contains(pubkey) && !self.banned_members.contains(pubkey)
    }

    /// Add an item to the shared playlist (`COLLABORATIVE` mode only).
    pub fn enqueue(&mut self, entry: QueueEntry) -> Result<(), CoreError> {
        if self.mode != RoomMode::Collaborative {
            return Err(CoreError::ModeMismatch);
        }
        self.queue_added.insert(entry.entry_id, entry);
        Ok(())
    }

    /// Remove an item from the shared playlist (`COLLABORATIVE` mode only).
    pub fn dequeue(&mut self, entry_id: Hash32) -> Result<(), CoreError> {
        if self.mode != RoomMode::Collaborative {
            return Err(CoreError::ModeMismatch);
        }
        self.queue_removed.insert(entry_id);
        Ok(())
    }

    /// Deterministic ordered shared queue (`(lamport, entry_id)`).
    pub fn ordered_queue(&self) -> Vec<&QueueEntry> {
        let mut items: Vec<&QueueEntry> = self
            .queue_added
            .values()
            .filter(|e| !self.queue_removed.contains(&e.entry_id))
            .collect();
        items.sort_by_key(|e| (e.lamport, e.entry_id));
        items
    }
}

/// Placeholder signature wrapper type alias for future Ed25519 helper ergonomics.
pub type RawSig = SignatureBytes;

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn democratic_room_rejects_queue_mutations() {
        let mut room = RoomState::new_empty(RoomMode::Democratic);
        let entry = QueueEntry {
            entry_id: [1; 32],
            stream_id: StreamId([2; 16]),
            proposer_pubkey: [3; 32],
            lamport: 1,
        };
        assert!(matches!(room.enqueue(entry), Err(CoreError::ModeMismatch)));
    }

    #[test]
    fn collaborative_queue_orders_deterministically_and_respects_tombstones() {
        let mut room = RoomState::new_empty(RoomMode::Collaborative);
        let e2 = QueueEntry {
            entry_id: [2; 32],
            stream_id: StreamId([2; 16]),
            proposer_pubkey: [9; 32],
            lamport: 2,
        };
        let e1 = QueueEntry {
            entry_id: [1; 32],
            stream_id: StreamId([1; 16]),
            proposer_pubkey: [8; 32],
            lamport: 1,
        };
        room.enqueue(e2.clone()).unwrap();
        room.enqueue(e1.clone()).unwrap();
        assert_eq!(room.ordered_queue().len(), 2);
        assert_eq!(room.ordered_queue()[0].entry_id, e1.entry_id);

        room.dequeue(e1.entry_id).unwrap();
        assert_eq!(room.ordered_queue().len(), 1);
        assert_eq!(room.ordered_queue()[0].entry_id, e2.entry_id);
    }

    #[test]
    fn option_b_kick_blocks_auth_even_if_admitted() {
        let mut room = RoomState::new_empty(RoomMode::Democratic);
        let pk = [7u8; 32];
        room.admitted_members.insert(pk);
        assert!(room.can_authenticate(&pk));

        room.apply_kick_ban(pk);
        assert!(!room.can_authenticate(&pk));
    }
}
