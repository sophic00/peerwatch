//! Normative v3 identifiers, wire constants, and capability types.

use serde::{Deserialize, Serialize};

/// Protocol major version (`docs/spec/protocol.md`).
pub const PROTOCOL_VERSION: u32 = 3;

/// Normative block transfer / Merkle leaf size in bytes (32 KiB).
pub const BLOCK_SIZE: usize = 32 * 1024;

/// Target keyframe-aligned segment duration in milliseconds (4 s).
pub const TARGET_SEGMENT_MS: u64 = 4_000;

/// Default maximum DataChannel envelope size in bytes (64 KiB).
pub const DEFAULT_MAX_ENVELOPE: usize = 64 * 1024;

/// Supported negotiated envelope size range (48 KiB..=256 KiB).
pub const MIN_MAX_ENVELOPE: usize = 48 * 1024;
pub const MAX_MAX_ENVELOPE: usize = 256 * 1024;

/// Aggregate SCTP `data + bulk` backpressure watermarks (128 KiB / 256 KiB).
pub const SCTP_LOW_WATERMARK: u32 = 128 * 1024;
pub const SCTP_HIGH_WATERMARK: u32 = 256 * 1024;

/// Maximum active peers in a v1 full-mesh room.
pub const MAX_ACTIVE_PEERS: usize = 16;

/// Maximum signaling relay frame size (32 KiB).
pub const MAX_RELAY_FRAME: usize = 32 * 1024;

/// Segment index 0 is always the initialization segment (`ftyp+moov` / EBML header).
pub const INIT_SEGMENT_INDEX: u32 = 0;

pub type Hash32 = [u8; 32];
pub type PublicKeyBytes = [u8; 32];
pub type EncPubKeyBytes = [u8; 32];
pub type SignatureBytes = [u8; 64];
pub type Alias = [u8; 16];
pub type ChannelId = [u8; 32];
pub type Term = u64;

/// Stable random 128-bit room identifier.
#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Hash, Serialize, Deserialize)]
pub struct RoomId(pub [u8; 16]);

/// Stable random 128-bit stream identifier (fixed at proposal/queue time).
#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Hash, Serialize, Deserialize)]
pub struct StreamId(pub [u8; 16]);

/// Peer identity hash (`BLAKE3("pw-peer-v3" || ed25519_pubkey)`).
#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Hash, Serialize, Deserialize)]
pub struct PeerId(pub Hash32);

impl PeerId {
    pub fn from_pubkey(pubkey: &PublicKeyBytes) -> Self {
        let mut hasher = blake3::Hasher::new();
        hasher.update(b"pw-peer-v3");
        hasher.update(pubkey);
        Self(*hasher.finalize().as_bytes())
    }

    /// Short hex prefix for human-readable logs (`pw:<8 hex chars>…`).
    pub fn short_display(&self) -> String {
        format!(
            "pw:{:02x}{:02x}{:02x}{:02x}…",
            self.0[0], self.0[1], self.0[2], self.0[3]
        )
    }
}

/// Governance mode chosen at room genesis.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
pub enum RoomMode {
    /// Proposals require a strict-majority vote; only the Host controls playback.
    Democratic,
    /// Shared CRDT queue and collaborative play/pause/seek forwarded through Host.
    Collaborative,
}

/// The three WebRTC DataChannels per peer connection.
#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Hash, Serialize, Deserialize)]
pub enum Channel {
    /// Reliable, ordered: handshake, CRDT digest, leases, governance, queue, sync, chat.
    Control,
    /// Reliable, ordered: STATE_SYNC batches, manifests, WebVTT subtitles.
    Bulk,
    /// Reliable, unordered: REQUEST, BLOCK, CANCEL, CHALLENGE_RESPONSE.
    Data,
}

/// Supported container profiles.
#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Hash, Serialize, Deserialize)]
pub enum Container {
    Fmp4,
    Webm,
}

/// Supported video codecs advertised in structured `Caps`.
#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Hash, Serialize, Deserialize)]
pub enum VideoCodec {
    H264,
    Vp9,
    Av1,
    Hevc,
}

/// Supported audio codecs advertised in structured `Caps`.
#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Hash, Serialize, Deserialize)]
pub enum AudioCodec {
    Aac,
    Opus,
}

/// Structured peer capabilities carried in `HELLO`.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct Caps {
    pub flags: u64,
    pub storage_budget_mb: u32,
    pub containers: Vec<Container>,
    pub video_codecs: Vec<VideoCodec>,
    pub audio_codecs: Vec<AudioCodec>,
    pub max_h264_level: u16,
}

impl Default for Caps {
    fn default() -> Self {
        Self {
            flags: 0,
            storage_budget_mb: 256,
            containers: vec![Container::Fmp4],
            video_codecs: vec![VideoCodec::H264],
            audio_codecs: vec![AudioCodec::Aac],
            max_h264_level: 41,
        }
    }
}

/// Normative per-segment metadata in a v3 manifest.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct SegmentEntry {
    pub seg_index: u32,
    pub t_start_ms: u64,
    pub t_end_ms: u64,
    pub byte_len: u32,
    pub segment_root: Hash32,
}

impl SegmentEntry {
    /// Number of ≤ 32 KiB blocks covering this segment.
    pub fn block_count(&self) -> u32 {
        let len = self.byte_len as usize;
        if len == 0 {
            0
        } else {
            len.div_ceil(BLOCK_SIZE) as u32
        }
    }

    /// Expected byte length for `block_index` inside this segment.
    pub fn expected_block_len(&self, block_index: u32) -> Option<usize> {
        let count = self.block_count();
        if block_index >= count {
            return None;
        }
        if block_index + 1 < count {
            Some(BLOCK_SIZE)
        } else {
            let rem = (self.byte_len as usize) % BLOCK_SIZE;
            Some(if rem == 0 { BLOCK_SIZE } else { rem })
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn segment_block_lengths_handle_exact_and_tail_blocks() {
        let seg = SegmentEntry {
            seg_index: 1,
            t_start_ms: 0,
            t_end_ms: 4000,
            byte_len: (BLOCK_SIZE as u32) * 2 + 123,
            segment_root: [0; 32],
        };
        assert_eq!(seg.block_count(), 3);
        assert_eq!(seg.expected_block_len(0), Some(BLOCK_SIZE));
        assert_eq!(seg.expected_block_len(1), Some(BLOCK_SIZE));
        assert_eq!(seg.expected_block_len(2), Some(123));
        assert_eq!(seg.expected_block_len(3), None);

        let exact = SegmentEntry {
            byte_len: (BLOCK_SIZE as u32) * 2,
            ..seg
        };
        assert_eq!(exact.block_count(), 2);
        assert_eq!(exact.expected_block_len(1), Some(BLOCK_SIZE));
    }
}
