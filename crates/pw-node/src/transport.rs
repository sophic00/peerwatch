//! Native Sans-I/O WebRTC transport built on `str0m`.
//!
//! Each peer connection owns one `str0m::Rtc` instance with three DataChannels
//! (`control`, `bulk`, `data`) and reports aggregate SCTP `bufferedAmount`
//! across `data + bulk` back to `pw-core`.

use pw_core::types::{SCTP_HIGH_WATERMARK, SCTP_LOW_WATERMARK};
use str0m::Rtc;

/// Per-peer WebRTC transport wrapper around `str0m::Rtc`.
pub struct Str0mPeer {
    rtc: Rtc,
    low_watermark: u32,
    high_watermark: u32,
}

impl Str0mPeer {
    /// Create a fresh `str0m` WebRTC state machine for one peer connection.
    pub fn new() -> Self {
        Self {
            rtc: Rtc::new(std::time::Instant::now()),
            low_watermark: SCTP_LOW_WATERMARK,
            high_watermark: SCTP_HIGH_WATERMARK,
        }
    }

    pub fn rtc(&self) -> &Rtc {
        &self.rtc
    }

    pub fn rtc_mut(&mut self) -> &mut Rtc {
        &mut self.rtc
    }

    /// True if aggregate `data + bulk` SCTP backlog exceeds the high watermark.
    pub fn is_backpressured(&self, aggregate_buffered_bytes: u32) -> bool {
        aggregate_buffered_bytes >= self.high_watermark
    }

    /// True if aggregate `data + bulk` SCTP backlog has drained below low watermark.
    pub fn backpressure_relieved(&self, aggregate_buffered_bytes: u32) -> bool {
        aggregate_buffered_bytes <= self.low_watermark
    }
}

impl Default for Str0mPeer {
    fn default() -> Self {
        Self::new()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn str0m_peer_constructs_and_checks_watermarks() {
        let peer = Str0mPeer::new();
        assert!(!peer.is_backpressured(SCTP_LOW_WATERMARK));
        assert!(peer.is_backpressured(SCTP_HIGH_WATERMARK));
        assert!(peer.backpressure_relieved(SCTP_LOW_WATERMARK));
    }
}
