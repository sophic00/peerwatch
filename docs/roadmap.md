---
tier: design
---

# Roadmap

## 1. Phases

Each phase ends with something **demonstrably working**, in dependency order.
Nothing later is started before its foundation exists.

The ordering is deliberate: authority and consistency come before media and
sync, because a media pipeline built on an unsettled authority model produces
behaviour that cannot be specified, only demonstrated. M0 fixes the
foundation first, in a simulator, with no network and no video.

Every phase lists its **demo**: the thing you can run at the end. A phase
without a passing demo is not done.

### M0 — Protocol schema, simulator, authority model

> One normative schema and a deterministic multi-peer simulation. No
> WebRTC, no video.

- `pw-proto`: canonical CBOR, envelope, message catalog, limits, manifest
  head/chunks, Merkle proofs ([protocol.md](spec/protocol.md)). Types are
  generated from [`schema/messages.yaml`](schema/messages.yaml) so the wire
  table and the Rust structs cannot drift.
- `pw-crypto`: Ed25519 + X25519 identities, AUTH transcript, certificates,
  derived epoch keys ([identity.md](design/identity.md)).
- `pw-core`: the pure `Event`/`Command` machine of
  [core-api.md](spec/core-api.md); host lease/term, frozen rosters,
  `VoteCertificate`/`TakeoverCertificate`, replay windows, and the five
  state machines of [architecture.md](design/architecture.md) §5.1.
- `pw-errors` (or a module): the codes in [errors.md](spec/errors.md), so
  every shell renders the same failure the same way.
- `pw-sim`: N virtual peers, fake clock, lossy/reordering network, churn,
  partitions, malicious behaviours. Property tests for “no two hosts in a
  term”, “no lost verified piece”, “certificates converge”, “rekey is
  convergent”, “offline peer recovers via lobby”.
- **Demo**: `pw-sim` runs a 5-peer room, passes a video proposal, loses the
  host, elects a complete peer by certificate, survives a partition, and
  recovers a peer that was offline across a rekey.

### M1 — Connectivity spine (proof of the mesh)

> Two peers, one file, no video.

- `pw-signal-client` (wasm-compatible) + `pw-signal-relay` (axum WS).
- Invite grants, `JOIN_REQUEST`/`JOIN_ACCEPT`, room key/epoch, per-peer
  `SignalEnvelope` encryption.
- Transports: native (`webrtc-rs`) and browser (main-thread
  `RTCPeerConnection`) behind the Transport adapter.
- AUTH handshake, DTLS fingerprint binding, relay limits/aliases.
- **STUN + TURN configuration from day one.** A TURN server (or a
  documented self-host path) is required for “across NATs”; it is not an
  M4 polish item.
- **Demo**: CLI peer ↔ browser peer exchange a file's blocks end-to-end
  (block Merkle proofs verified), across NATs, with the relay blind and a
  TURN fallback available.

### M2 — Data plane and storage

> The swarm moves bytes reliably before it moves video.

- Manifest head/chunks/appends, piece roots, block Merkle proofs.
- Scheduler: window + rarest-first, capacity-proportional selection,
  BDP-adaptive Q, `bufferedAmount` backpressure, CANCEL, hedged re-requests.
- Native append-only store + journal; browser OPFS pack files; bitfield
  persistence/scrub.
- Roaring bitfields and validation.
- **Demo**: 4 peers pre-seed a synthetic 1 GB file, one peer joins late and
  fetches it with block-level verification; a poisoning peer is banned
  without false positives.

### M3 — Media pipeline, players, sync

> First real watch party.

- fMP4 segmenter, remux + transcode-ahead (ffmpeg), WASM transmuxer.
- Room-wide codec/capability intersection; FMP4 default, WEBM only with
  unanimous codec support.
- MSE player (main thread) + mpv adapter; desktop control RPC + local media
  protocol.
- `SYNC`/`START_AT` with local receive anchor, corrected PI controller,
  buffering gate.
- **Demo**: 1 CLI host + 2 browser peers watch a transcoded-from-MKV video
  in sync; a Safari/WebKitGTK peer is not stranded by a WebM proposal.

### M4 — Governance, host lifecycle, kicks

> The product spec: proposals, votes, host leases, kicks.

- `PROPOSAL`/`VOTE`/`VOTE_CERT`, frozen rosters, certificate convergence.
- `PLAY_VIDEO` host transitions with `HOST_LEASE`.
- Free-roam + return-to-live; host-leave: degraded → challenge → takeover
  certificate → stalled → rejoin.
- `KICK` + `KickCertificate` + banlist + rekey/epoch rotation.
- Local block-proof bans + advisory `BAN_REPORT`.
- **Demo**: host leaves mid-film, a complete peer takes over with a
  majority certificate; a kicked peer cannot return with the old key or old
  epoch; a partition cannot create two hosts.

### M5 — Hardening, social features, scale headroom

- Full byte-rate/strike matrix, scoring refinements
  (power-of-two-choices), endgame mode, TOFU known-peers display,
  passphrase-at-rest for native keys, bandwidth/RAM tuning from telemetry.
- **Text chat** polish: persistence, notifications, moderation affordances.
- **Voice chat**: Opus media tracks on the existing RTCPeerConnections.
  Browser: `getUserMedia`/`addTrack`; native: `cpal` capture → `opus`
  encode → `webrtc-rs` sample tracks; per-receiver decode+mix, push-to-talk
  default. Capability-negotiated (`VOICE` flag), so voice-less peers
  interoperate.
- Partial-mesh overlay exploration (>16 peers), LAN discovery (mDNS) as a
  signaling-free path, subtitle/multi-audio tracks, optional ABR ladder.

---

## 2. Decision log

Moved to [design-log.md](design/design-log.md), where each decision records
the alternative that was rejected and why. Twenty-eight decisions (D1–D28)
plus thirty-two corrections (C1–C32) found during the rewrite.

If you are about to change a constant, add a message type, or re-open a
closed question, read that file first.

---

## 3. Explicit non-goals (v1)

- Rooms >16 peers as a *supported* configuration.
- Public/discoverable rooms, server-side room directories, accounts.
- Mobile apps (browser on mobile *may* work, not a target).
- Subtitle tracks, multi-audio, ABR ladders (single-quality stream).
- Original-quality side-channel downloads.
- General Byzantine consensus or anonymity against a global observer.
- Partial-mesh gossip (designed for later, not specified in v1).

---

## 4. Telemetry that will validate or kill assumptions

Collect (locally, opt-in) during M2–M5 dogfooding:

- cold-start latency, stall count/duration per session;
- host-uplink saturation %, takeover frequency/latency;
- transcode-ahead underruns and CPU load;
- Q adaptation distribution and `bufferedAmount` high-water marks;
- CANCEL volume per seek;
- certificate convergence time and failed-takeover reasons;
- block-proof failure rate and false-ban reports;
- browser memory (hot tier + MSE + WASM) and quota failures.

Each maps to a row in [performance.md](design/performance.md). If reality
disagrees, the model (and the constants) get revised, not the other way
around.
