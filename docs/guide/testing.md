---
tier: guide
---

# Testing & Verification Strategy

PeerWatch tests deterministic protocol logic in isolation first, then validates
real `str0m` ↔ browser interop, media pipelines, and adversarial edge cases.

---

## 1. Deterministic simulation (`pw-sim`)

`pw-core` is pure Sans-I/O, so `pw-sim` runs N virtual peers over a fake
monotonic clock, controllable loss/jitter/partition network, and injectable
store faults. Same seed ⇒ byte-identical command trace and `debug_snapshot()`.

Key property tests (`proptest`) & scenarios:

- **CRDT convergence**: arbitrary partition/reorder/duplicate delivery of
  `RoomGenesis`, `MemberAdd`, `KickCertificate`, `PeerSignalKey`, `HostLease`,
  and `QueueAdd`/`QueueRemove` converges to identical state on every honest
  peer.
- **Lease safety**: at most one authoritative `HostLease` per `(stream_id,
  term)`; stale-term or non-host `SYNC` triggers `PW_AUTHORITY_VIOLATION`.
- **Dual room modes**: `DEMOCRATIC` rejects `CONTROL_REQ`/`QUEUE_*`
  (`PW_MODE_MISMATCH`); `COLLABORATIVE` forwards `CONTROL_REQ` cleanly through
  Host `SYNC` and auto-advances queue leases.
- **2-step takeover**: host loss → `TAKEOVER_CLAIM` → optional `CHALLENGE` →
  majority `TAKEOVER_ACK` → `HOST_LEASE(term+1)`; minority partitions stall
  safely without split-brain; signed `BYE` shrinks active roster cleanly.
- **Option B kicks**: verified `KickCertificate` permanently blocks future
  `SignalEnvelope` and `AUTH` from the kicked key.
- **Data plane & storage**: bad `BLOCK` proof triggers instant local ban with
  zero false positives; verified blocks are servable from RAM immediately while
  crash-recovery tests assert persisted on-disk indices never reference
  unflushed segments.
- **Sync controller**: converges from ±2 s offset and ±0.1% clock skew without
  overshoot, hard-seeks above 2.0 s, stays clamped within ±5%.

---

## 2. Golden vectors & fuzzing

- **Checked-in canonical CBOR golden vectors** pin `peer_id`, `room_channel`,
  `alias`, `AUTH` transcript, `segment_root` / Merkle proofs (including 1-block
  and odd-leaf segments), `manifest_hash`, `content_id`, and `InviteEnvelope`
  so native and `wasm32` targets stay bit-identical.
- **Fuzz targets** (`cargo-fuzz`) cover every untrusted parser with
  pre-allocation assertions: canonical CBOR decoder, `AVAILABILITY` bitmap/RLE,
  Merkle verifier, fMP4 box / WebM EBML segmenter ([protocol.md](../spec/protocol.md)
  §9.1), `SignalEnvelope`/`InviteEnvelope`, and `ffprobe` JSON.

---

## 3. Transport, browser E2E, and chaos tests

- **M0/M1 `str0m` ↔ Browser E2E** (Playwright Chromium/Firefox/WebKit): invite
  join through `pw-relay` (+ forced-TURN via local `coturn`), DTLS-bound
  `AUTH`, 32 KiB block transfers, aggregate SCTP `bufferedAmount` pacing, MSE
  fMP4 + WebVTT playback, `QuotaExceededError` recovery, OPFS pack persistence.
- **Native daemon E2E**: local web UI server token check, `mpv` range server
  token check + `observe_property("time-pos")`, ffmpeg remux & HW/SW transcode
  + `SeekSource`.
- **Chaos & soak**: kill `pw-relay` mid-stream (mesh keeps playing), kill Host
  mid-film (takeover completes), 16-peer soak with 2% packet loss and 150 ms
  RTT.

---

## 4. CI gates

1. `cargo fmt --check` and `cargo clippy --workspace --all-targets -- -D warnings`
2. `cargo test --workspace` (unit tests + `pw-sim` deterministic scenarios)
3. `cargo check -p pw-core -p pw-wasm --target wasm32-unknown-unknown`
4. `python3 scripts/lint_invariants.py` and `python3 scripts/gen_catalog.py --check`
5. `cargo deny check` + short `fuzz-smoke` + Playwright E2E on release/PRs
