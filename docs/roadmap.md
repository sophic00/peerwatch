---
tier: design
---

# Roadmap

Each milestone ends with a concrete, runnable demo. The v3 ordering de-risks
the hardest platform boundary (`str0m` ↔ browser WebRTC + MSE playback) right
in M0 instead of waiting until late sprints.

---

## 1. Milestones

### M0 — Core contract, simulator skeleton, and `str0m` ↔ Browser spike

- **6-crate workspace**: `pw-core`, `pw-wasm`, `pw-node`, `pw-cli`, `pw-relay`,
  `pw-sim` with CI checks (`fmt`, `clippy -D warnings`, `test`, `wasm32` check
  on `pw-core` + `pw-wasm`, doc linter).
- **`pw-core` foundation**: canonical CBOR framing, envelope, identifiers,
  domain-separated BLAKE3 hashing, per-segment Merkle trees, Ed25519/X25519
  identities, single-signature DTLS-bound `AUTH`, CRDT room state (`RoomGenesis`,
  `MemberAdd`, `KickCertificate`, `HostLease`, `PeerSignalKey`, queue), and
  `pw-sim` deterministic harness with property tests.
- **Risk-retirement spike**: minimal `str0m` native peer ↔ browser tab
  DataChannel (`control`/`bulk`/`data`) exchanging 32 KiB Merkle-verified
  blocks and feeding a short fMP4 segment into browser MSE.
- **Demo**: `pw-sim` passes CRDT merge + lease convergence property tests, and
  one native peer streams a synthetic fMP4 clip to a browser tab over `str0m` ↔
  WebRTC.

### M1 — First 2-peer watch party (end-to-end vertical slice)

- **`pw-relay`**: stateless `axum` WebSocket mailbox with size/rate/IP limits.
- **Invites & deadlock-free join**: `pw3_...` tokens, zero-cleartext
  `InviteEnvelope`, `JoinAccept`, self-signed `PEER_SIGNAL_KEY`, STUN + local
  `coturn` TURN test.
- **Media pipeline (`pw-node`)**: `ffprobe`, remux, HW-accelerated
  transcode-ahead (VA-API/NVENC/VideoToolbox/QSV + x264 fallback), fMP4
  segmenter (init = seg 0), audio track selection, WebVTT subtitle extraction.
- **Playback & sync**: `web/` UI (MSE player with `AppendToken` backpressure +
  subtitles), native local web UI server (`127.0.0.1/?token=...`) + `mpv`
  adapter (`observe_property`), min-delay `SYNC` clock filter, retuned PI
  controller (`Kp=0.30, Ki=0.0225`, ±5% clamp, 2 s hard-seek), Free-Roam.
- **Demo**: Native daemon hosts a local MKV (transcoded with subtitles) and a
  browser tab joins via invite through `pw-relay` and watches in lockstep sync.

### M2 — Multi-peer swarm & storage tiers

- **Full mesh (up to 16 peers)** with aggregate SCTP `bufferedAmount` pacing
  (128 / 256 KiB watermarks).
- **Scheduler**: `INIT`/`URGENT`/`WINDOW`/`SEEK`/`BACKFILL` (rarest-first),
  capacity-proportional selection, BDP-adaptive `Q`, hedging, queue-only
  `CANCEL`, endgame, `AVAILABILITY` (compact raw/RLE + active `frontier` block
  masks).
- **Storage**: RAM hot tier (immediate serving of verified blocks) + crash-safe
  native pack store (`index.cbor` + `index.journal` + scrub + 20 GiB LRU) +
  browser OPFS pack files (sliding window + rare-reserve + quota fallback).
- **Seekable live transcode**: `MediaCommand::SeekSource` restarting ffmpeg on
  out-of-frontier host seeks.
- **Demo**: 4 peers stream a 1080p film across native + browser peers; a
  poisoning peer sending one bad block is banned locally with zero false
  positives.

### M3 — Governance, dual modes, chat & hardening

- **`DEMOCRATIC` mode**: `PROPOSAL`, live `PROPOSAL_COMPAT`, `VOTE`,
  `VoteCertificate` (equivocation rejection), active-roster pruning on signed
  `BYE`, 2-step `TAKEOVER_CLAIM` / `CHALLENGE` / `TAKEOVER_ACK`.
- **`COLLABORATIVE` mode**: shared CRDT playlist (`QUEUE_ADD`/`QUEUE_REMOVE`),
  auto-advance `QueueClaim` leases, and `CONTROL_REQ` playback forwarding.
- **Moderation & social**: `KICK` certificates (Option B banlist exclusion),
  chat (dedup + Lamport order), CLI `ratatui` TUI option.
- **Browser proposals**: JS transmux adapter (`mp4box.js`/`mediabunny`) for
  browser-proposed MP4/WebM.
- **Hardening**: cargo-fuzz targets (CBOR, Merkle, fMP4/EBML parser limits,
  signaling), 16-peer soak & chaos tests (relay kill, host kill, partition/heal,
  packet loss), `mpv` OS sandboxing (`bubblewrap`/`firejail`), `pw doctor`.
- **Demo**: 5-peer room runs both Democratic and Collaborative sessions,
  survives mid-film Host departure via 2-step takeover, and excludes a kicked
  peer cleanly.

### M4 (v2 / Post-v1)

- **Tauri desktop shell**: thin native window wrapping the `pw-node` local
  daemon API.
- **Voice chat**: Opus media tracks on the existing `str0m` / WebRTC
  connections (push-to-talk default, `VOICE` capability flag).
- **Scale & LAN extras**: partial-mesh gossip (>16 peers), LAN mDNS discovery,
  ABR ladder, passphrase-at-rest for native keys, TOFU safety numbers.
