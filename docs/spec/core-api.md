---
tier: normative
---

# Core API (normative)

`pw-core` is the whole protocol as a **synchronous, deterministic state
machine**. Every platform — CLI, desktop, browser Worker, and the simulator —
drives it through the same two enums. This document is the contract between
the core and those shells.

The wire format is in [protocol.md](protocol.md). This document is about the
*in-process* boundary: what goes in, what comes out, and what the shell owes
the core.

---

## 1. The loop

```rust
pub trait Core {
    fn handle(&mut self, event: Event) -> Vec<Command>;
    fn next_timeout(&self) -> Option<Instant>;
}
```

`handle` is **pure**: same input state + same event ⇒ same output commands,
always. No I/O, no clock reads, no RNG, no threads, no `async`. Everything
external arrives as an `Event` and leaves as a `Command`.

The shell runs one loop:

```
loop {
    if let Some(t) = core.next_timeout(), and t <= now {
        cmds = core.handle(Event::Timer(t));
    } else {
        ev   = select(next_io_event(), next_ui_event());
        cmds = core.handle(ev);
    }
    for cmd in cmds { shell.execute(cmd); }
}
```

`next_timeout` is how the core asks to be woken without knowing what a clock
is. A shell that ignores it will appear to work and then silently stop
retransmitting, stop hedging, and stop heartbeating. Shells MUST honour it.

### 1.1 Determinism requirements

The core MUST NOT:

- read the wall clock (time arrives as `Event::Tick { now_ms }` and is stored
  in core state; all timers are relative to it);
- read entropy (randomness arrives as `Event::Entropy` or is derived from a
  seeded CSPRNG the shell installed at construction);
- touch the filesystem, network, environment, or process table;
- spawn threads or hold a lock across `handle`;
- iterate a `HashMap` where order affects output (use `BTreeMap` or sort).

These are not style rules. `pw-sim` (see [testing.md](../guide/testing.md))
replays a seed and requires a byte-identical command trace; any of the above
breaks that, and the resulting flakiness is very hard to attribute later.

---

## 2. `Event` — what goes in

Events are grouped by origin. Every variant is listed; the core ignores
unknown `#[non_exhaustive]` variants, so adding one is a minor change
([protocol.md](protocol.md) §10).

```rust
pub enum Event {
    // ---- lifecycle ------------------------------------------------------
    Start { config: CoreConfig, identity: Identity, now_ms: u64 },
    Shutdown,

    // ---- time and entropy ----------------------------------------------
    Tick { now_ms: u64 },              // monotonic; also refreshes all timers
    Timer(TimerId),                    // fired by the shell per next_timeout()
    Entropy { bytes: Vec<u8> },        // optional; only if config.seed is None

    // ---- transport ------------------------------------------------------
    PeerConnected    { peer: PeerId, channel: Channel },
    PeerDisconnected { peer: PeerId, reason: DisconnectReason },
    MessageReceived  { peer: PeerId, channel: Channel, bytes: Bytes },
    MessageFlushed   { peer: PeerId, channel: Channel, bytes: u32 },  // SCTP backpressure relief
    PeerBackpressure { peer: PeerId, channel: Channel, buffered_bytes: u32 },

    // ---- signaling (relay) ---------------------------------------------
    RelayConnected,
    RelayDisconnected { reason: String },
    RelayRoster      { channel: ChannelId, aliases: Vec<AliasEntry> },
    RelayEnvelope    { channel: ChannelId, from_alias: Alias, bytes: Bytes },
    RelayBasis       { channel: ChannelId, bytes: Bytes },

    // ---- store ----------------------------------------------------------
    BlockStored  { piece: u32, block: u16, outcome: PutOutcome },
    BlockLoaded  { piece: u32, block: u16, data: Bytes },
    BlockMissing { piece: u32, block: u16 },
    StoreError   { piece: u32, block: u16, error: StoreError },
    StoreBudget  { budget: StoreBudget },
    EvictDone    { freed_bytes: u64 },
    ScrubResult  { piece: u32, ok: bool },

    // ---- player ---------------------------------------------------------
    Player(PlayerEvent),               // see §4

    // ---- media pipeline (native proposer only) -------------------------
    MediaProbed  { stream: StreamId, info: MediaInfo },
    MediaSegment { stream: StreamId, index: u32, bytes: Bytes, t_start_ms: u64, t_end_ms: u64 },
    MediaProgress{ stream: StreamId, encoded_ms: u64, playhead_ms: u64 },
    MediaError   { stream: StreamId, error: MediaError },
    MediaDone    { stream: StreamId, total_bytes: u64, duration_ms: u64 },

    // ---- UI / local control ---------------------------------------------
    Ui(UiCommand),                     // see §5

    // ---- simulation only (never constructed by a production shell) ------
    #[cfg(feature = "sim")]
    SimInject { peer: PeerId, message: Bytes, delay_ms: u64 },
}
```

### 2.1 `Channel`

```rust
pub enum Channel { Control, Bulk, Data }
```

Matches the three DataChannels in [protocol.md](protocol.md) §1.2. The shell
owns the mapping to `RTCDataChannel` / `webrtc-rs`; the core only names them.

### 2.2 Why `MessageFlushed` and `PeerBackpressure` exist

`bufferedAmount` is transport state the core cannot see, but the scheduler
must respect it ([distribution.md](../design/distribution.md) §4.3). Rather
than let the shell queue unboundedly, the shell reports it:

- `PeerBackpressure` is delivered when `bufferedAmount` crosses
  `high_watermark`; the core stops issuing `Send` for that channel until it
  receives `MessageFlushed` below `low_watermark`.
- The core MUST NOT assume `Send` is synchronous. A `Send` is a *request*;
  the shell may drop it on disconnect, which surfaces as
  `PeerDisconnected`.

---

## 3. `Command` — what comes out

```rust
pub enum Command {
    // ---- transport ------------------------------------------------------
    Send {
        peer: PeerId,
        channel: Channel,
        bytes: Bytes,              // one complete envelope
        class: SendClass,          // Control | Bulk | Data — for rate limiting
    },
    ClosePeer { peer: PeerId, code: ByeCode, reason: String },

    // ---- signaling ------------------------------------------------------
    RelaySubscribe   { channel: ChannelId, alias: Alias, signal_pub: EncPubKey },
    RelayUnsubscribe { channel: ChannelId },
    RelaySend        { to: Alias, bytes: Bytes },
    RelayBasisPublish{ channel: ChannelId, body: Bytes },

    // ---- store ----------------------------------------------------------
    StoreBlock  { piece: u32, block: u16, data: Bytes },
    LoadBlock   { piece: u32, block: u16 },
    Invalidate  { piece: u32 },
    FlushStore,
    Evict       { policy: EvictionPolicy },

    // ---- player ---------------------------------------------------------
    Player(PlayerCommand),         // see §4

    // ---- media pipeline -------------------------------------------------
    Media(MediaCommand),           // see §6

    // ---- timers ---------------------------------------------------------
    SetTimer   { id: TimerId, after_ms: u64 },
    CancelTimer{ id: TimerId },

    // ---- UI -------------------------------------------------------------
    EmitUi { event: UiEvent },     // see §5

    // ---- effects the shell must make visible ---------------------------
    Notify { level: NotifyLevel, title: String, body: String },
}
```

`SendClass` exists so the shell can apply the per-class token buckets of
[protocol.md](protocol.md) §9 without re-parsing the envelope. The core
already enforces them (§3.1); the shell's copy is a second line of defence
against a core bug.

### 3.1 Ordering and atomicity

- Commands are executed **in order**. A `StoreBlock` before a `Send` that
  advertises the piece is a bug in the core, not a licence for the shell to
  reorder.
- The core MAY emit a `Send` whose delivery the shell later fails; the core
  MUST tolerate that (it is indistinguishable from loss on an unordered
  channel).
- `FlushStore` is a durability barrier: the core MUST NOT emit a `BITFIELD`
  claiming a piece until it has seen the matching `BlockStored` **and** a
  subsequent `FlushStore` completed. This is the in-process mirror of
  [storage.md](../design/storage.md) §1.3.
- A single `handle` call MAY emit commands that are mutually contradictory
  (e.g. `StoreBlock` then `Invalidate` for the same piece after a scrub
  failure). That is legal and intentional: the shell executes both.

### 3.2 What the core does *not* decide

- **Transport selection.** ICE, TURN, SDP — entirely shell-side
  ([connectivity.md](../design/connectivity.md)). The core sees
  `PeerConnected`, never a candidate pair.
- **Codec probing.** `MediaProbed` carries the answer; the core does not run
  ffprobe.
- **Layout and copy.** `Bytes` is whatever the shell's allocator gives it.
- **Retry policy for the relay.** The core emits `RelaySubscribe`; backoff is
  shell-side ([config.md](../guide/config.md) §5).

---

## 4. Player contract

`PlayerCommand` / `PlayerEvent` are defined in
[frontends.md](../design/frontends.md) §1, including the append-token
backpressure protocol. The core-side obligations:

| Core sends | Core must have already | Core expects |
|---|---|---|
| `Load { init_segment }` | verified every block of the init segment | `State { buffering: true }` |
| `AppendSegment { token }` | verified every block overlapping the segment | exactly one `AppendDone` or `AppendRejected` for that token |
| `Seek { t_ms }` | resolved `t_ms` to a segment via the table | `SeekComplete { t_ms }` |
| `SetRate { rate }` | clamped to `[0.9, 1.1]` | nothing (fire and forget) |
| `Evict { up_to_ms }` | nothing | `BufferedAhead` reflecting the reduction |

The core MUST NOT send `AppendSegment` for a segment whose pieces are
incomplete. A partial segment is not decodable and feeding it to MSE
produces a decode error that looks like a media bug.

`Position` and `BufferedAhead` are the controller's inputs
([sync.md](../design/sync.md) §3–4). A player adapter that reports
`Position` less than 10 Hz makes the PI loop jittery; more than 30 Hz is
waste. The recommended rate is 20 Hz, or `timeupdate` plus a 50 ms timer.

---

## 5. UI contract

The web UI is shared between the browser build and the Tauri build, so the
core cannot emit platform-specific UI. It emits **semantic events**; the UI
decides presentation.

```rust
pub enum UiEvent {
    RoomJoined   { room_id: RoomId, epoch: u64, roster: Vec<PeerView> },
    RosterChanged{ roster: Vec<PeerView> },
    HostChanged  { host: Option<PeerId>, term: u64, reason: HostChangeReason },

    ProposalOpened { proposal: ProposalView },
    ProposalClosed { proposal_id: Hash32, outcome: VoteOutcome, certificate: Option<Hash32> },
    VoteProgress   { proposal_id: Hash32, approvals: u32, rejections: u32, needed: u32 },

    PlaybackState { state: PlayState, t_media_ms: u64, rate: f32, host: Option<PeerId> },
    FreeRoam      { delta_ms: i64 },          // negative = behind live
    BufferHealth  { ahead_ms: u64, state: BufferState },
    SyncError     { drift_ms: i64, corrected: bool },

    StreamStarted { stream: StreamView },
    StreamProgress{ pieces_have: u32, pieces_total: u32, bytes_per_s: u64 },
    StreamEnded   { stream: StreamId, reason: StreamEndReason },

    ChatMessage   { from: PeerId, body: String, ts_ms: u64, lamport: u64 },
    PeerStats     { peer: PeerId, down_bps: u64, up_bps: u64, score: f32 },

    Toast { level: NotifyLevel, text: String },
    Error { code: ErrorCode, text: String },   // codes in errors.md
}

pub enum UiCommand {
    JoinRoom   { invite: String },
    CreateRoom { invites: u32 },
    LeaveRoom,

    ProposeFile { path: String },      // native only
    ProposeBlob { name: String, size: u64 },  // browser: File handle acquired shell-side
    CastVote   { proposal_id: Hash32, approve: bool },

    Play, Pause, Seek { t_ms: u64 }, SetRate { rate: f32 },
    EnterFreeRoam, ReturnToLive,

    SendChat { body: String },
    Kick { peer: PeerId, reason: String },
    SetUploadPolicy { policy: UploadPolicy },
}
```

### 5.1 Rules

- `UiEvent` is **derived state, never authority**. A UI that misses an event
  must be correctable by a full `RoomJoined`-shaped snapshot; the core MUST
  therefore be able to re-emit the current view on demand
  (`UiCommand::RequestSnapshot`, implied by `RoomJoined`).
- The core MUST NOT emit more than `ui_rate_limit` (default 20/s) `UiEvent`s
  excluding `PeerStats` and `ChatMessage`, which are batched per tick.
- `PeerStats` is aggregated per tick, not per message. A naive implementation
  that emits one `PeerStats` per `BLOCK` will saturate the UI bridge.
- `UiCommand::Seek` is a *local* seek: in `FOLLOWING` it enters `FREE_ROAM`
  ([sync.md](../design/sync.md) §5). The core MUST NOT forward it to the room.
- `ProposeFile` / `ProposeBlob` carry a *handle*, not bytes. The shell owns
  the file. In the browser this is a `File` object passed through the Worker
  bridge once, not streamed through `UiCommand`.

---

## 6. Media command contract

Native only; the browser shell never receives these.

```rust
pub enum MediaCommand {
    Probe  { stream: StreamId, path: PathBuf },
    Start  { stream: StreamId, path: PathBuf, pipeline: Pipeline, target: StreamParams },
    Throttle { stream: StreamId, max_encode_ahead_ms: u64 },
    Stop   { stream: StreamId },
}

pub enum Pipeline { Remux, TranscodeAhead { preset: String, crf: u8, maxrate: String } }
```

`Start` is the only command that causes work proportional to file size.
`Throttle` is how the segmenter's 120 s read-ahead cap
([media-pipeline.md](../design/media-pipeline.md) §3.2) is enforced: the core
computes `playhead + 120 s − encoded_ms` and sends it every tick while a
transcode is active. A shell that ignores `Throttle` will encode the whole
file and fill the disk.

---

## 7. Timers

```rust
pub struct TimerId(u32);
```

Timer ids are core-allocated and opaque. The core MUST:

- reuse ids after `CancelTimer` (bounded set, no leak);
- never depend on a timer firing exactly on time — `Event::Timer` may arrive
  late, and the core reads `now_ms` from the last `Tick`;
- be able to operate with a single shared timer if the shell collapses
  timers (`next_timeout` returns the earliest).

The shell MUST:

- call `Tick` at least every 100 ms (heartbeats, RTT estimation, and the PI
  loop all degrade below that);
- deliver `Tick` with a monotonic millisecond value that never goes
  backwards. A shell that uses wall-clock time will jump on NTP steps; use a
  monotonic source.

| Platform | Monotonic source | Timer mechanism |
|---|---|---|
| CLI / desktop | `std::time::Instant` | tokio `sleep_until` |
| Browser Worker | `performance.now()` | `setTimeout` (throttled in background tabs — see below) |
| `pw-sim` | virtual, advanced by the harness | instant |

**Background tabs.** A hidden tab throttles `setTimeout` to ≥ 1 s. The core
MUST tolerate `Tick` gaps of up to 60 s without corrupting state: timers
fire late, heartbeats are skipped, and the peer is reported `AWAY`
([frontends.md](../design/frontends.md) §2.2 step 8). A gap longer than
`away_timeout` (default 60 s) makes the peer `STALE` and it stops being a
takeover candidate until it re-syncs.

---

## 8. Core state and snapshotting

The core owns exactly this state (nothing else is authoritative):

| Group | Contents |
|---|---|
| Identity | `peer_id`, keys, `room_key`, `epoch`, `signal_pub` |
| Room | roster, banlist, `state_version`, `term`, `host_lease`, proposals |
| Streams | `stream_id` → manifest head/version, piece roots, segment table |
| Swarm | per-peer bitfield, availability table, throughput/latency EWMAs, scores, strikes |
| Sync | reference accumulator, PI integrator, mode (`FOLLOWING`/`FREE_ROAM`), last `SYNC` |
| Scheduler | in-flight requests, hedges, `Q` per peer |
| Store view | local bitfield, pending writes, scrub queue |
| Player view | last `Position`/`BufferedAhead`/`State` |

`Core::snapshot()` and `Core::restore()` are **not** required for v1: a peer
that restarts rejoins and rebuilds from `ROOM_STATE` + its persisted
bitfield. What *is* required is that the above groups can be dumped as a
deterministic, canonical-CBOR blob for the simulator's trace comparison and
for bug reports. `pw-sim` compares snapshots, not just command streams,
because two different states can emit the same commands for a while.

---

## 9. Conformance

A `pw-core` implementation is conformant if, for every scenario in the
`pw-sim` corpus, it produces:

1. the same ordered command trace, and
2. the same snapshot at every checkpoint,

as the reference implementation, given the same seed, config, and scripted
events. See [testing.md](../guide/testing.md) §1 and
[conformance.md](../guide/conformance.md).

Traces are canonical CBOR so they diff cleanly and can be committed as
golden files.
