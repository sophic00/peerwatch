//! Native RAM hot tier + crash-safe segment pack store (`docs/design/storage.md`).

use std::collections::BTreeMap;

use bytes::Bytes;
use pw_core::types::StreamId;

/// In-memory verified segment cache (M0 scaffold; disk pack + journal arrive in M2).
#[derive(Debug, Default)]
pub struct MemorySegmentStore {
    segments: BTreeMap<(StreamId, u32), Bytes>,
}

impl MemorySegmentStore {
    pub fn put_verified_segment(&mut self, stream: StreamId, seg: u32, bytes: Bytes) {
        self.segments.insert((stream, seg), bytes);
    }

    pub fn get_segment(&self, stream: StreamId, seg: u32) -> Option<&Bytes> {
        self.segments.get(&(stream, seg))
    }

    pub fn invalidate(&mut self, stream: StreamId, seg: u32) {
        self.segments.remove(&(stream, seg));
    }
}
