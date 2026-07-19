# Roadmap & Decision Log

## 1. Phases

Each phase ends with something **demonstrably working**, in dependency
order. Nothing later is started before its foundation exists.

### M1 — Connectivity spine (proof of the mesh)

> Two peers, one file, no video.

- Cargo workspace, `pw-core` skeleton (envelope, identity, store trait,
  zero-I/O discipline).
- `pw-signal`: signaling relay (axum WS) + client, token derive (HKDF).
- Transports: native (`webrtc-rs`) and browser (`web-sys`) behind the
  Transport trait.
- AUTH handshake, encrypted signaling envelopes.
- **Demo**: CLI peer ↔ browser peer exchange a file's pieces end-to-end
  (SHA-256 verified), across NATs, with the relay blind.

### M2 — Watch party, minimal

> First real room: one fixed video, play/pause/seek, browsers welcome.

- Manifest + segment table, fMP4 segmenter, remux + transcode-ahead
  pipelines (ffmpeg), WASM transmuxer for browser sources.
- Scheduler (window + rarest-first, capacity-proportional selection,
  BDP-adaptive Q, CANCEL, hedged re-requests).
- MSE player in the shared web UI, mpv adapter for CLI, storage backends
  (sparse file / memory+OPFS).
- SYNC + PI controller, buffering gate + START_AT.
- **Demo**: 1 CLI host + 2 browser peers watch a transcoded-from-MKV video
  in sync.

### M3 — Governance & resilience

> The product spec: proposals, votes, host lifecycle.

- PROPOSAL/VOTE/VOTE_RESULT, PLAY_VIDEO host transitions, KICK + banlists.
- Free-roam + return-to-live, host-leave handling: degraded → takeover →
  stalled → rejoin.
- Local auto-bans (poison/sigfail) + BAN_REPORT gossip.
- **Demo**: host leaves mid-film, a complete peer takes over seamlessly,
  a kicked peer cannot return with the same key.

### M4 — Hardening & polish

- Full strike/rate-limit matrix, scoring refinements (power-of-two-choices),
  endgame mode, TURN config path, TOFU known-peers display,
  passphrase-at-rest for native keys, bandwidth/RAM tuning from telemetry.

### M5 — Social features & scale headroom

- **Text chat** lands properly (CHAT exists from M1 envelopes, M5 gives it
  persistence, notifications, moderation affordances).
- **Voice chat**: Opus media tracks on the existing RTCPeerConnections.
   Browser: `getUserMedia`/`addTrack`, native: `cpal` capture → `opus`
   encode → `webrtc-rs` sample tracks, per-receiver decode+mix, push-to-talk
   default. Capability-negotiated (`VOICE` flag), so voice-less peers
   interoperate. *(Confirmed: post-MVP by product decision.)*
- Partial-mesh overlay exploration (>16 peers), LAN discovery (mDNS) as a
  signaling-free path, expiring invite tokens, subtitle track support.

## 2. Decision log

| # | Decision | Alternatives considered | Why this won |
|---|----------|------------------------|--------------|
| D1 | **Rust everywhere** (one core → native + WASM) | Go + separate TS stack, Go-native + thin-browser | Browser peers must run the full stack, only Rust serves native+WASM from one codebase. Go would fork the implementation forever. |
| D2 | **WebRTC as the only transport** | Raw TCP/QUIC for native, WebRTC just for browser | One protocol for all pairs, browsers force WebRTC anyway, DTLS encryption free. |
| D3 | **Tiny signaling relay** (blind WS) | Manual copy-paste signaling, DHT | 3+-person rooms need hands-free rendezvous, relay sees only ciphertext and is self-hostable. Manual fallback kept possible by transport-agnostic envelopes. |
| D4 | **Canonical format: fMP4 H.264/AAC** (+ WEBM profile) | Share originals, transcode everything to WebM | MSE universality + mpv compatibility + lossless remux path, WEBM kept only for browser-sourced files that can't transmux. |
| D5 | **Remux if possible, else live transcode-ahead** | Transcode fully before sharing | Minutes of pre-watch delay is unacceptable, ~15 s head start + throttled-ahead encoder starts fast. Preset: **x264 veryfast** (confirmed). |
| D6 | **Host = file owner, chosen by vote** | Creator-host, moderator roles | Product spec, matches "propose → vote → play". Creator gets zero privileges. |
| D7 | **Host controls room transport, per-peer free-roam** | Anyone-controls, host-only with no escape | Social clarity + personal freedom: roam locally, "return to live" re-syncs (confirmed). |
| D8 | **Governance = signed majority votes, no roles** | Moderator/veto models | No trust anchor needed, every peer tallies independently, cryptographic offenses skip the vote entirely. |
| D9 | **Sync = PI controller on playback rate** | v1's 3-tier thresholds, full PID, NTP-style locks | Zero steady-state error (I kills clock-skew), no audible steps, ζ=1 no overshoot, D excluded (noise). Kp=0.04, Ki=0.0004. |
| D10 | **Piece 512 KiB / block 64 KiB / segment 4 s** | Bigger pieces (fewer hashes), per-segment pieces | Manifest size vs verify granularity vs DataChannel-friendly messages, 4 s = DASH-sweet-spot seek granularity. |
| D11 | **Full mesh ≤16 peers** | Partial mesh from day one, SFU | Simplicity and latency at social scale, protocol doesn't assume full connectivity → partial mesh later without breakage. |
| D12 | **Desktop = Tauri + shared web UI + MSE, mpv auto-fallback** | mpv-always, native GUI (egui/iced) | One UI for browser+desktop, integrated experience, fallback only when WebKitGTK lacks gst codecs (confirmed). |
| D13 | **Storage: native sparse file / browser memory+OPFS sliding window** | Browser full-download, IDB blobs | Browsers can't be full archivers at 7 GB/film, sliding window + rare-reserve keeps them net contributors. |
| D14 | **Ed25519 identity + room_psk authorization** | Passwords, PAKE, relay-side ACLs | No server state, signatures double as vote/governance proofs, token = capability. |
| D15 | **Voice chat post-MVP (M5)** | In MVP | DataChannel-first keeps M1–M4 shippable, media tracks slot into existing connections via capability flags (confirmed). |

## 3. Explicit non-goals (v1)

- Rooms >16 peers as a *supported* configuration.
- Public/discoverable rooms, server-side room directories, accounts.
- Mobile apps (browser on mobile *may* work, not a target).
- Subtitle tracks, multi-audio, ABR ladders (single-quality stream).
- Original-quality side-channel downloads.

## 4. Telemetry that will validate or kill assumptions

Collect (locally, opt-in) during M2–M4 dogfooding: cold-start latency,
stall count/duration per session, host-uplink saturation %, takeover
frequency/latency, transcode-ahead underruns, Q adaptation distribution,
CANCEL volume per seek. Each maps to a row in
[performance.md](performance.md). If reality disagrees, the model (and the
constants) get revised, not the other way around.
