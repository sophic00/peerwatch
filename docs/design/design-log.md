---
tier: design
---

# Design Log

Why PeerWatch v3 looks the way it does, which foundational v2 decisions were
retained, and what was redesigned in v3 (along with rejected alternatives).

---

## Part A — Retained Foundational Decisions (D1–D20)

- **D1. Rust for the whole core (`native` + `wasm32`)**: one Sans-I/O protocol
  implementation (`pw-core`) serves both native daemons and browser Workers.
- **D2. WebRTC everywhere**: browsers can only make direct P2P links via
  WebRTC, so native peers speak WebRTC too.
- **D3. Browser main-thread / Worker split**: `RTCPeerConnection` and MSE live
  on the main thread; `pw-core` (WASM), crypto, and OPFS sync handles live in
  a Worker bridged by transferable `ArrayBuffer`s.
- **D4. Full mesh (5–16 peers)**: lowest latency and simplest topology at
  watch-party scale; partial mesh deferred past v1.
- **D5. fMP4 H.264/AAC default (+ unanimous WebM/VP9/Opus)**: broad MSE
  compatibility plus native `mpv` playback with zero dual distribution.
- **D6. Remux when possible, live transcode-ahead otherwise**: lossless copy
  when compatible; throttled live transcode otherwise.
- **D7. 32 KiB blocks & 4 s keyframe-aligned segments**: 32 KiB block + Merkle
  proof + CBOR framing stays safely inside 48–64 KiB DataChannel envelopes;
  4 s is the DASH/HLS seek sweet spot.
- **D8. Per-block Merkle proofs**: exact local blame against the sending peer
  before a segment finishes; prevents framing honest peers.
- **D9. Stable `stream_id`, append-only manifest, final `content_id`**: live
  transcoding grows the manifest without invalidating in-flight requests or
  storage keys.
- **D10. Deterministic CBOR**: canonical wire hashing across implementations
  with strict pre-allocation depth/size bounds.
- **D11. Separate `control`, `bulk`, `data` DataChannels**: keeps large
  manifests and state sync from stream-level head-of-line blocking `SYNC`.
- **D12. Capacity-proportional weighted-random peer selection**:
  `c_i = θ_i · max(0, 1 − u_i) · r_i` avoids `argmax` herding and distributes
  load proportionally to true capacity.
- **D13. BDP-adaptive `Q ∈ [4, 64]`**: prevents silently capping fast WAN
  links while bounding memory.
- **D14. Queue-only `CANCEL` on seek**: kills queued waste while storing
  already-in-flight valid blocks.
- **D15. Single clock authority per `(stream_id, term)`**: unambiguous
  reference clock; per-peer Free-Roam provides the local escape hatch.
- **D16. Continuous PI rate controller (no deadband)**: eliminates standing
  deadband error, audible step changes, and systematic hardware clock skew.
- **D17. Local monotonic receive anchor (no Host wall clock)**: immune to
  unsynchronized system clocks.
- **D18. Free-Roam as a sync state, not membership**: paused/rewound viewers
  stay in the swarm and keep uploading and voting.
- **D19. Signed, expiring, usage-capped invites**: prevents room-wide bearer
  token Sybil minting.
- **D20. Voice chat deferred to v2**: data-channel-first keeps v1 shippable;
  `str0m` and `RTCPeerConnection` cleanly add Opus media tracks later.

---

## Part B — v3 Architectural Redesign Decisions (D21–D32)

### D21. Collapse 3-level (`Segment / Piece / Block`) → 2-level (`Segment → Block`)

- **Chosen**: Segment (`0` = init, `1..=N` = ≈ 4 s keyframe-aligned media) IS
  the integrity and availability unit, with a per-segment BLAKE3 Merkle tree
  over 32 KiB blocks.
- **Rejected**: v2's unaligned 512 KiB pieces cutting across variable-length
  segments + Portable Roaring bitmaps.
- **Why**: unaligned pieces lacked byte offsets in `SegmentEntry` (breaking
  MSE segment slicing), coupled adjacent segments during seek/eviction, stalled
  live-transcode publishing on partial trailing pieces, ballooned 100 GB
  manifests to 6.4 MB / 400 chunks, and required a Roaring bitmap spec/crate.
  At ~1,800 segments per 2-hour movie, the manifest is ~100 KiB (~4 chunks),
  the raw bitmap is ≤ 226 B (8 B RLE when complete), and every segment has
  exact `[0..byte_len]` boundaries.

### D22. Decouple RAM block availability from disk `fdatasync`

- **Chosen**: verified 32 KiB blocks in RAM are advertised immediately via
  `AVAILABILITY.frontier` and served right away; disk durability (`fdatasync`
  data → `fdatasync` journal) gates only the persisted crash-recovery index.
- **Rejected**: blocking network piece advertising behind two `fdatasync`s.
- **Why**: putting disk fsync barriers in the live swarm fan-out path adds
  unnecessary latency without improving crash safety; crash recovery only reads
  the persisted on-disk index.

### D23. Replace monolithic host-signed `ROOM_STATE` with CRDT signed fact set

- **Chosen**: room state is the join-semilattice (union + max) of
  self-verifying signed records (`RoomGenesis`, `MemberAdd`, `KickCertificate`,
  `PeerSignalKey`, `HostLease`, `QueueAdd`/`QueueRemove`), synced via
  `STATE_DIGEST` / `STATE_SYNC`.
- **Rejected**: host-authored `ROOM_STATE` snapshots with `banlist_complete`,
  `banlist_root`, and two-peer omission cross-checks.
- **Why**: removes single-envelope size limits, eliminates `term=0`
  `state_version` races, makes banlist withholding impossible by construction,
  and makes partition healing a trivial set union.

### D24. Deadlock-free `SignalEnvelope` & zero-cleartext `InviteEnvelope`

- **Chosen**: peers self-sign `PEER_SIGNAL_KEY`, and a new joiner embeds its
  `MemberAdd` + `sender_signal_pub` inside its encrypted, signed initial
  `SignalEnvelope`. `InviteEnvelope` removes `room_id`/`invite_id` from the
  cleartext header and uses XChaCha20-Poly1305 AEAD.
- **Rejected**: requiring `signal_pub` from host-signed `ROOM_STATE` before
  answering an SDP offer; putting `room_id` in `InviteEnvelope`'s header.
- **Why**: v2 deadlocked on the very first join (existing peers couldn't reply
  until the joiner appeared in `ROOM_STATE`, which required connecting first),
  and cleartext `room_id` in `InviteEnvelope` defeated relay privacy.

### D25. Option B kicks — drop room-key rotation, epochs, and stateful relay lobby

- **Chosen**: one `room_secret` per room, 100% stateless relay (no lobby
  channel or 24 h retention), honest kick exclusion enforced by verified CRDT
  `KickCertificate` banlist at signaling and `AUTH`.
- **Rejected**: v2's derived `room_key(e+1) = HKDF(room_key(e), ...)` +
  `REKEY` + `rekey_grace` + stateful `LOBBY_BASIS`.
- **Why**: v2's own spec admitted a kicked peer holding `room_key(e)` could
  derive `room_key(e+1)` and the new channel from the public lobby, while
  ephemeral DTLS + ephemeral X25519 already provide forward secrecy for all
  data and signaling. Dropping it deletes ~15% of protocol state machines and
  keeps `pw-relay` purely stateless.

### D26. Single-signature `AUTH` and local-only block blame

- **Chosen**: `AUTH` carries only its transcript-bound `auth_sig` + `auth_tag`
  (no redundant outer envelope signature). Bad `BLOCK` Merkle proofs trigger an
  immediate local ban; transferable `BlockProof` / `BAN_REPORT` are removed.
- **Rejected**: double-signing `AUTH`; requiring `BlockProof.sender_sig` while
  keeping `BLOCK` unsigned.
- **Why**: an unsigned `BLOCK` made `BlockProof.sender_sig` impossible to
  construct in v2. Local blame is already exact and instant over authenticated
  DTLS; room-wide removal uses standard `KICK` proposals.

### D27. Dual room modes (`DEMOCRATIC` + `COLLABORATIVE` / Queue) & active-roster pruning

- **Chosen**: rooms select `DEMOCRATIC` (vote per video, Host-only controls) or
  `COLLABORATIVE` (shared CRDT queue + `CONTROL_REQ` forwarded through Host).
  Signed `BYE` prunes departing peers from the active roster before freezing
  future proposal/takeover snapshots.
- **Rejected**: democratic-only rooms with permanently stale frozen rosters.
- **Why**: friends watching multi-episode series want a shared queue and shared
  pause/seek without losing single-clock sync, and clean departures should not
  permanently deadlock later takeover quorums.

### D28. Clean 2-step Host takeover (no pre-committed `challenge_commitment`)

- **Chosen**: `TAKEOVER_CLAIM` → optional per-peer `CHALLENGE` /
  `CHALLENGE_RESPONSE` → majority `TAKEOVER_ACK` → `HOST_LEASE(term+1)`.
- **Rejected**: embedding `challenge_commitment` inside the initial
  `TAKEOVER_CLAIM` before challenges are issued.
- **Why**: fixes v2's causality contradiction while preserving proof of
  possession and strict-majority split-brain protection.

### D29. Switch native WebRTC from `webrtc-rs` to `str0m` + aggregate SCTP pacing

- **Chosen**: **`str0m`** (pure-Rust Sans-I/O WebRTC) + aggregate
  `bufferedAmount` watermarks (128 / 256 KiB across `data` + `bulk`).
- **Rejected**: `webrtc-rs` + per-channel-only backpressure.
- **Why**: `str0m` matches `pw-core`'s Sans-I/O model, shrinks dependencies,
  improves browser interop, and aggregate SCTP pacing prevents `data`/`bulk`
  from bufferbloating `SYNC`/`PING` on `control`.

### D30. Drop Tauri from v1 → Native Daemon + Local Web UI (real browser) + `mpv`

- **Chosen**: two v1 frontends — Browser WASM App and Native Daemon serving the
  exact same `web/` UI over `127.0.0.1` (plus `--player mpv` / CLI / TUI).
  Tauri desktop wrapper moves to **v2**.
- **Rejected**: shipping Tauri/WebKitGTK + GStreamer codec bundling + external
  mpv fallback in v1.
- **Why**: eliminates Linux WebKitGTK MSE codec fragmentation and packaging
  overhead while giving Linux, macOS, and Windows users native disk/ffmpeg
  power with rock-solid browser HW video decoding.

### D31. Media & sync refinements (HW transcode, subtitles, JS browser transmux, retuned PI)

- **Chosen**: dynamic `PROPOSAL_COMPAT` (`isTypeSupported`), HW-accelerated
  transcode-ahead (VA-API/NVENC/VideoToolbox/QSV + x264 fallback), seekable
  live transcode, WebVTT subtitles + audio track selection in v1, JS browser
  transmux adapter (`mp4box.js`/`mediabunny`), sliding-window min-delay `SYNC`
  filter, and retuned PI gains (`Kp = 0.30, Ki = 0.0225`, ±5% clamp, 2.0 s
  hard-seek, ~11 s 50% settling).
- **Rejected**: ≤ 16 verbatim codec strings, software-only x264, custom
  Rust/WASM transmuxer in v1, 42 s PI settling with audible ±10% clamp.
- **Why**: directly addresses real-world MKV watch-party usability and human
  sync perception.

### D32. Collapse 13 crates → 6 crates and de-risk WebRTC + MSE early

- **Chosen**: `pw-core`, `pw-wasm`, `pw-node`, `pw-cli`, `pw-relay`, `pw-sim`;
  prove `str0m` ↔ Browser DataChannel + MSE playback in M0.
- **Rejected**: 13 crates and waiting until M3/Sprint 6 to play a video frame.
- **Why**: maintains 100% clean WASM dependency isolation with far less
  workspace overhead, and proves the riskiest platform boundary first.
