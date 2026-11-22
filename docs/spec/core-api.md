---
tier: normative
---

# Core API (normative)

`pw-core` is the entire protocol as a **synchronous, deterministic Sans-I/O
state machine**. Every shell — the native daemon (`pw-node` / `pw-cli`), the
browser Worker (`pw-wasm`), and the deterministic simulator (`pw-sim`) — drives
it through one uniform `Event` / `Command` boundary.

Wire formats live in [protocol.md](protocol.md). This document defines the
in-process contract.

---

## 1. The loop

```rust
pub trait Core {
    fn handle(&mut self, event: Event) -> Vec<Command>;
    fn next_timeout(&self) -> Option<u64>; // monotonic ms
}
```

`handle` is pure: identical state + identical `Event` always yields identical
`Command`s.

### 1.1 Determinism rules

`pw-core` MUST NOT:

- read wall clocks or `Instant::now()` (time arrives via `Event::Tick { now_ms }`);
- read ambient OS randomness (entropy is injected via a seeded CSPRNG at startup
  or `Event::Entropy`);
- touch files, sockets, threads, or environment variables;
- iterate a `HashMap` where iteration order affects command output or hashes
  (use `BTreeMap` / `BTreeSet` or sort).

---

## 2. `Event` — inputs from shell to core

```rust
pub enum Event {
    // ---- lifecycle ------------------------------------------------------
    Start { config: CoreConfig, identity: Identity, now_ms: u64 },
    Shutdown,

    // ---- time and entropy ----------------------------------------------
    Tick { now_ms: u64 },
    Timer { id: TimerId, now_ms: u64 },
    Entropy { bytes: Vec<u8> },

    // ---- transport (str0m native / RTCPeerConnection browser) ----------
    PeerConnected      { peer: PeerId, dtls_fingerprint: [u8; 32] },
    PeerChannelOpen    { peer: PeerId, channel: Channel },
    PeerDisconnected   { peer: PeerId, reason: DisconnectReason },
    MessageReceived    { peer: PeerId, channel: Channel, bytes: Bytes },
    /// Aggregate SCTP buffered bytes across `data` + `bulk` channels.
    TransportBackpressure { peer: PeerId, aggregate_buffered_bytes: u32 },

    // ---- signaling (stateless relay) -----------------------------------
    RelayConnected,
    RelayDisconnected  { reason: String },
    RelayRoster        { channel: ChannelId, aliases: Vec<Alias> },
    RelayEnvelope      { channel: ChannelId, from_alias: Alias, bytes: Bytes },

    // ---- store (segment-addressed, RAM + durable backend) --------------
    BlockLoaded        { stream: StreamId, seg: u32, block: u16, data: Bytes },
    BlockMissing       { stream: StreamId, seg: u32, block: u16 },
    SegmentDurable     { stream: StreamId, seg: u32 },
    StoreError         { stream: StreamId, seg: u32, error: StoreError },
    StoreBudget        { budget: StoreBudget },
    EvictDone          { freed_bytes: u64 },
    ScrubResult        { stream: StreamId, seg: u32, ok: bool },

    // ---- player ---------------------------------------------------------
    Player(PlayerEvent),

    // ---- media pipeline (native ffmpeg or browser JS transmuxer) -------
    MediaProbed        { stream: StreamId, info: MediaInfo },
    MediaSegment       { stream: StreamId, seg: u32, t_start_ms: u64, t_end_ms: u64, bytes: Bytes },
    MediaProgress      { stream: StreamId, encoded_ms: u64 },
    MediaError         { stream: StreamId, error: MediaError },
    MediaDone          { stream: StreamId, total_bytes: u64, duration_ms: u64 },

    // ---- UI / local control ---------------------------------------------
    Ui(UiCommand),
}
```

### 2.1 `Channel` and aggregate SCTP backpressure

```rust
pub enum Channel { Control, Bulk, Data }
```

Because `control`, `bulk`, and `data` share one SCTP congestion window
([protocol.md](protocol.md) §1.2), the shell reports **aggregate**
`bufferedAmount` across `data` + `bulk` via `TransportBackpressure`:

- Above **256 KiB** (high watermark): `pw-core` pauses emitting `Data` and
  `Bulk` sends to that peer.
- Below **128 KiB** (low watermark): `pw-core` resumes issuing `Data`/`Bulk`
  sends.
- `Control` sends (`SYNC`, `PING`, `VOTE`) remain unthrottled so sync latency
  stays low.

---

## 3. `Command` — outputs from core to shell

```rust
pub enum Command {
    // ---- transport ------------------------------------------------------
    ConnectPeer        { peer: PeerId, alias: Alias },
    Send               { peer: PeerId, channel: Channel, bytes: Bytes, class: SendClass },
    ClosePeer          { peer: PeerId, code: ByeCode, reason: String },

    // ---- signaling ------------------------------------------------------
    RelaySubscribe     { channel: ChannelId, alias: Alias },
    RelayUnsubscribe   { channel: ChannelId },
    RelaySend          { channel: ChannelId, to: Alias, bytes: Bytes },

    // ---- store ----------------------------------------------------------
    /// Persist an already-verified segment (all blocks verified in RAM).
    PersistSegment     { stream: StreamId, seg: u32, bytes: Bytes },
    LoadBlock          { stream: StreamId, seg: u32, block: u16 },
    InvalidateSegment  { stream: StreamId, seg: u32 },
    FlushStore,
    Evict              { policy: EvictionPolicy },

    // ---- player ---------------------------------------------------------
    Player(PlayerCommand),

    // ---- media pipeline -------------------------------------------------
    Media(MediaCommand),

    // ---- timers ---------------------------------------------------------
    SetTimer           { id: TimerId, after_ms: u64 },
    CancelTimer        { id: TimerId },

    // ---- UI -------------------------------------------------------------
    EmitUi             { event: UiEvent },
}
```

### 3.1 Ordering and durability

- Commands are executed in order.
- **RAM-first serving, disk-safe resume**: `pw-core` verifies every incoming
  32 KiB block against `segment_root` in RAM and MAY serve verified blocks to
  peers immediately (via `AVAILABILITY.frontier` / `BLOCK`) without waiting for
  `FlushStore`. The storage backend MUST only update its persisted crash-safe
  index after `PersistSegment` + `FlushStore` complete (emitting
  `SegmentDurable`), guaranteeing a crash never leaves a persisted bit set for
  unflushed data ([storage.md](../design/storage.md) §1.3).

---

## 4. Player contract

`PlayerCommand` / `PlayerEvent` are defined in
[frontends.md](../design/frontends.md) §1, including the `AppendToken` and
`writable_bytes()` backpressure contract.

| Core sends | Core guarantees | Core expects |
|---|---|---|
| `Load { mime, init_segment, subtitles }` | every block of segment `0` verified | `State { buffering: true }` |
| `AppendSegment { seg, bytes, token }` | every block of segment `seg` verified | one `AppendDone` or `AppendRejected` per token |
| `Seek { t_ms }` | resolved to segment start keyframe | `SeekComplete { t_ms }` |
| `SetRate { rate }` | clamped to `[0.95, 1.05]` | fire-and-forget |
| `Evict { up_to_ms }` | none | updated `BufferedAhead` |

- Player adapters MUST report `Position` at **≥ 10 Hz** (recommended 20 Hz via
  `timeupdate` + 50 ms timer in MSE, or `observe_property("time-pos")` in mpv)
  so the PI sync controller runs smoothly.

---

## 5. UI contract

The `web/` UI runs identically in pure Browser WASM mode and when served
locally by the native daemon.

```rust
pub enum UiCommand {
    CreateRoom { mode: RoomMode, invites: u32 },
    JoinRoom   { invite: String },
    LeaveRoom,
    MintInvite { max_uses: u32, ttl_s: u64 },

    ProposeFile { path: String, audio_stream: Option<u32>, sub_streams: Vec<u32> },
    ProposeBlob { name: String, size: u64 },
    CastVote    { proposal_id: Hash32, approve: bool },

    // Collaborative mode queue + room controls
    QueueAdd    { path_or_blob: String },
    QueueRemove { entry_id: Hash32 },
    RoomPlay, RoomPause, RoomSeek { t_ms: u64 }, RoomSetRate { rate: f32 },

    // Local controls (enter FreeRoam if following)
    LocalPause, LocalSeek { t_ms: u64 }, ReturnToLive,

    SendChat    { body: String },
    Kick        { peer: PeerId, reason: String },
    SetUploadPolicy { policy: UploadPolicy },
    RequestSnapshot,
}

pub enum UiEvent {
    RoomSnapshot   { room_id: RoomId, mode: RoomMode, roster: Vec<PeerView>, queue: Vec<QueueEntryView> },
    RosterChanged  { roster: Vec<PeerView> },
    HostChanged    { host: Option<PeerId>, term: u64, reason: HostChangeReason },
    QueueChanged   { queue: Vec<QueueEntryView> },

    ProposalOpened { proposal: ProposalView },
    ProposalCompat { proposal_id: Hash32, peer: PeerId, playable: bool },
    ProposalClosed { proposal_id: Hash32, outcome: VoteOutcome },
    VoteProgress   { proposal_id: Hash32, approvals: u32, rejections: u32, needed: u32 },

    PlaybackState  { state: PlayState, t_media_ms: u64, rate: f32, host: Option<PeerId> },
    FreeRoam       { delta_ms: i64 },
    BufferHealth   { ahead_ms: u64, state: BufferState },
    SyncStats      { drift_ms: i64, applied_rate: f32 },

    StreamStarted  { stream: StreamView },
    StreamProgress { segments_have: u32, segments_total: u32, down_bps: u64, up_bps: u64 },
    StreamEnded    { stream: StreamId, reason: StreamEndReason },

    ChatMessage    { from: PeerId, body: String, ts_ms: u64, lamport: u64 },
    PeerStats      { stats: Vec<PeerStatRow> },
    Error          { code: ErrorCode, text: String },
}
```

- `UiEvent` is derived view state, never authority; `RequestSnapshot` rebuilds
  the full UI view at any time (e.g., on browser reload or new local web tab).
- `PeerStats` is batched once per second (never per block).

---

## 6. Media command contract

```rust
pub enum MediaCommand {
    Probe      { stream: StreamId, source: MediaSourceRef },
    Start      { stream: StreamId, source: MediaSourceRef, pipeline: Pipeline,
                 audio_stream: Option<u32>, sub_streams: Vec<u32>, start_ms: u64 },
    SeekSource { stream: StreamId, target_ms: u64 }, // seekable live transcode
    Throttle   { stream: StreamId, max_encode_ahead_ms: u64 },
    Stop       { stream: StreamId },
}

pub enum Pipeline {
    Remux,
    TranscodeAhead { encoder: VideoEncoder, crf: u8, maxrate: String },
    BrowserTransmux,
}

pub enum VideoEncoder { AutoHw, Vaapi, Nvenc, VideoToolbox, Qsv, LibX264 }
```

---

## 7. Timers and background tabs

- Shells MUST deliver monotonic `Event::Tick { now_ms }` at least every 100 ms
  while foregrounded (`std::time::Instant` on native, `performance.now()` in
  Worker, virtual clock in `pw-sim`).
- Hidden browser tabs throttle `setTimeout` to ≥ 1 s. `pw-core` tolerates tick
  gaps up to 60 s cleanly, marking the peer `AWAY` without corrupting state or
  striking peers.
- For deterministic debugging and simulator replay, `pw-core` exposes a
  canonical-CBOR `debug_snapshot()` covering identity, CRDT room state, active
  stream manifest, swarm availability, sync controller, and scheduler queues.
