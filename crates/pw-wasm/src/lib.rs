//! Browser Web Worker WASM shell (`pw-wasm`).
//!
//! Runs `pw-core` inside a dedicated browser Worker alongside Worker-only
//! OPFS synchronous pack storage and IndexedDB identity keys, bridged to the
//! main thread (`RTCPeerConnection` + MSE) via transferable `ArrayBuffer`s.

use pw_core::api::{CoreConfig, Event};
use pw_core::{Core, PeerCore};
use wasm_bindgen::prelude::*;

/// Handle owned by the browser Web Worker driving `pw-core`.
#[wasm_bindgen]
pub struct WasmPeerWorker {
    core: PeerCore,
}

#[wasm_bindgen]
impl WasmPeerWorker {
    #[wasm_bindgen(constructor)]
    pub fn new() -> Self {
        Self {
            core: PeerCore::new(CoreConfig::default()),
        }
    }

    /// Advance the core monotonic clock (`performance.now()` rounded to ms).
    pub fn tick(&mut self, now_ms: u64) -> usize {
        self.core.handle(Event::Tick { now_ms }).len()
    }

    /// Current monotonic core timestamp in milliseconds.
    pub fn now_ms(&self) -> u64 {
        self.core.now_ms()
    }
}

impl Default for WasmPeerWorker {
    fn default() -> Self {
        Self::new()
    }
}
