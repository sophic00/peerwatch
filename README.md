# PeerWatch

**A serverless, peer-to-peer watch party.** A group of 5–16 friends joins an
ephemeral room, selects local videos democratically or through a shared
collaborative queue, and distributes the stream BitTorrent-style over encrypted
WebRTC while playing back in lockstep.

No media server. No accounts. No persistent server state.

```
   Browser App (WASM + WebRTC + MSE + OPFS)  ←─────┐
                                                    ├── full mesh, encrypted WebRTC
   Native Daemon (str0m + ffmpeg + local UI / mpv) ←┘
```

## Status

**v3 architecture locked and 6-crate workspace scaffolded.** Ready for M0
implementation (`pw-core` protocol/CRDT engine + `pw-sim` + `str0m` ↔ browser
MSE vertical spike). Track milestones in [docs/roadmap.md](docs/roadmap.md) and
[plan.md](plan.md).

## Key architectural pillars (v3)

1. **Pure Sans-I/O Rust core (`pw-core`)**: zero I/O, zero ambient clocks or
   RNG; compiles identically to native and `wasm32-unknown-unknown`, verified
   in a deterministic multi-peer simulator (`pw-sim`).
2. **`str0m` ↔ Browser WebRTC**: pure-Rust Sans-I/O WebRTC natively, built-in
   `RTCPeerConnection` on the browser main thread, aggregate SCTP backpressure
   pacing so bulk transfers never delay playback `SYNC`.
3. **2-level segment-aligned media (`Segment → Block`)**: Segment `0` is the
   fMP4 init segment, `1..=N` are ≈ 4 s keyframe-aligned media segments, each
   carrying a BLAKE3 Merkle root over 32 KiB blocks (~100 KiB manifest and
   ≤ 226 B availability bitmap for a 2-hour 4K film), plus optional WebVTT
   subtitles and HW-accelerated seekable live transcode-ahead.
4. **CRDT room state & dual modes**: room membership, `KICK` bans, signaling
   keys, host leases, and collaborative queues converge via set union of
   self-verifying signed records. Rooms support both **Democratic** (vote per
   video) and **Collaborative** (shared queue & controls) modes.
5. **Stateless signaling & honest security**: `pw-relay` holds zero disk state
   and zero lobby history; invites carry zero cleartext room metadata; kicks
   are enforced cleanly by verified `KickCertificate` banlists at signaling and
   DTLS-bound `AUTH`, while DTLS + ephemeral X25519 provide forward secrecy.
6. **Two clean v1 frontends**: a zero-install **Browser App** (`web/` +
   `pw-wasm`) and a cross-platform **Native Daemon** (`peerwatch`) that serves
   the exact same `web/` UI locally to your real browser (HW-accelerated MSE
   with no WebKitGTK codec issues) and optionally drives `mpv` / CLI / TUI.
   Tauri desktop packaging is planned for v2.

## Workspace layout (6 crates)

```
crates/
  pw-core/   pure Sans-I/O protocol, canonical CBOR, crypto, CRDT state,
             manifest/Merkle, scheduler, sync, errors (native + wasm32)
  pw-wasm/   wasm-bindgen browser Worker shell + OPFS pack store
  pw-node/   native Tokio runtime: str0m WebRTC, disk store, ffmpeg pipeline,
             mpv adapter, local HTTP/WS server, signaling client
  pw-cli/    `peerwatch` binary: daemon + local web UI host + CLI/TUI
  pw-relay/  stateless axum WebSocket signaling mailbox
  pw-sim/    deterministic multi-peer simulator & property tests
web/         shared web UI (Browser WASM mode & Native Daemon local mode)
docs/        v3 specification & guides (start at docs/README.md)
scripts/     catalog generator & documentation invariant linter
```

## Building & checking

Requires Rust 2024 edition (1.85+).

```sh
cargo test --workspace
cargo clippy --workspace --all-targets -- -D warnings
python3 scripts/lint_invariants.py
python3 scripts/gen_catalog.py --check
```

## License

MIT — see [LICENSE](LICENSE).
