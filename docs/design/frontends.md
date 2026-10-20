---
tier: design
---

# Frontends — CLI, Desktop, Browser

Three ways to be a peer. All run the same `pw-core` protocol state machines;
they differ in **how video is played**, **how UI is rendered**, and **what
the platform allows**. The normative transport and manifest details are in
[protocol.md](../spec/protocol.md). This document is the per-platform
handbook.

---

## 1. The player boundary

The core does not call a player through an `async fn` trait. It emits
**commands** and consumes **events** (the full event/command vocabulary is
normative in [core-api.md](../spec/core-api.md)):

```rust
enum PlayerCommand {
    Load { stream_id, params, init_segment },
    Play, Pause, Seek { t_ms }, SetRate { rate },
    AppendSegment { segment_index, bytes, token: AppendToken },
    Flush, Evict { up_to_ms: u64 },
}

enum PlayerEvent {
    Position { t_ms },
    BufferedAhead { ms },
    State { playing: bool, buffering: bool, ended: bool },
    SeekComplete { t_ms },
    AppendDone { token: AppendToken, accepted: bool },   // backpressure signal
    AppendRejected { token: AppendToken, reason: AppendReject },
    Error { code: ErrorCode, message: String },
}

trait PlayerAdapter {
    fn send(&self, cmd: PlayerCommand);
    fn poll(&self) -> Vec<PlayerEvent>;
    /// How many more bytes the player will accept right now.
    fn writable_bytes(&self) -> usize;
}
```

### 1.1 Why append needs a token and a completion event

`AppendSegment` is not fire-and-forget. On MSE, `SourceBuffer.appendBuffer`
is asynchronous and raises `updateend`; a second append before `updateend`
throws `InvalidStateError`, and a full buffer raises `QuotaExceededError`.
An interface with no completion event forces every adapter to invent its own
queue and its own error path, and gives the core no way to know whether the
bytes it thought it had fed are actually decodable.

So the contract is:

1. The core sends `AppendSegment { token, .. }` only while
   `writable_bytes() >= bytes.len()`.
2. The adapter MUST eventually emit exactly one of `AppendDone` or
   `AppendRejected` for each token.
3. `AppendRejected { reason: QuotaExceeded }` obliges the core to send
   `Evict { up_to_ms }` and retry once. It MUST NOT be treated as a peer
   error — a full `SourceBuffer` is local resource pressure, not
   misbehaviour.
4. A token that never completes within `append_timeout` (default 10 s) is a
   local player fault: the core pauses, reports `BUFFERING`, and re-creates
   the `SourceBuffer`.

`writable_bytes` is what makes the MSE backpressure loop explicit instead of
implicit. mpv implements it as a large constant (it has its own cache); MSE
implements it as `SourceBuffer` headroom minus a safety margin.

This is object-safe and works across the Rust/JS boundary. The browser MSE
player is implemented in TypeScript/JS; the native mpv adapter is Rust. The
core’s sync/scheduler logic is identical; only the adapter differs.

| Frontend | Player adapter | UI |
|----------|----------------|----|
| Browser | **MSE player** in the main thread (web UI) | web UI (full) |
| Desktop (Tauri) | **MSE player** in the webview *(default)*, **mpv adapter** *(fallback)* | same web UI in webview |
| CLI | **mpv adapter** | terminal (ratatui) + mpv window |

---

## 2. Browser

### 2.1 Architecture

```
main thread:   web UI (DOM, <video>, MSE SourceBuffers)
               RTCPeerConnection + RTCDataChannel
               capability detection (MediaSource.isTypeSupported)
               autoplay gesture / visibility / persistence prompts
worker:        pw-core (WASM) — scheduler, store, sync, governance
               crypto, OPFS sync file access, block verification,
               segment assembly
bridge:        MessagePort: control JSON/typed messages +
               transferable ArrayBuffers for media/data
```

**Why this split:** `RTCPeerConnection` is a `Window` API, not a `Worker`
API. Exposing WebRTC in workers is still an open W3C issue; Firefox and
Safari do not provide it. OPFS sync access, on the other hand, only exists
in Workers. The original design put WebRTC in the Worker and was therefore
not portable. The realistic split is:

- **main thread** owns the network and the player, because that is where the
  browser exposes them;
- **Worker** owns the CPU-heavy, storage-heavy core, because that is where
  OPFS and SHA/BLAKE3 work belong.

The bridge must not copy media repeatedly. Use `postMessage` with
transferable `ArrayBuffer`s; if `SharedArrayBuffer` is used, the page must
be cross-origin isolated (COOP/COEP headers) and the deployment must say so.

### 2.2 The MSE player

1. On `MANIFEST_HEAD`: `new MediaSource()`, `addSourceBuffer(params.codecs)`
   (e.g. `video/mp4; codecs="avc1.640028,mp4a.40.2"`).
2. Append the **init segment** as soon as its blocks verify.
3. Append media segments in decode order as their pieces complete; keep
   30–60 s appended ahead of the playhead, evict behind
   (`SourceBuffer.remove`) on memory pressure.
4. **Seek**: `remove()` the buffered ranges around the target, jump the
   scheduler's window to the seek segment
   ([distribution.md](distribution.md)), append from the segment start
   (keyframe).
5. Rate control = `video.playbackRate` (the PI controller's actuator,
   [sync.md](sync.md)); position = `video.currentTime`; buffer health =
   `video.buffered`.
6. Handle `QuotaExceededError` on `appendBuffer` by evicting; never let a
   full `SourceBuffer` kill the room.
7. Autoplay: browsers block audio without a user gesture. Require a
   **“Join / Start watching” click**; the buffering gate starts after it.
8. Background tabs: use Page Visibility to mark `AWAY`, reduce upload, and
   re-sync on return. A hidden tab may be throttled by the browser.

### 2.3 Browser-specific limits

- **Proposals**: mp4/webm only, validated by
  `MediaSource.isTypeSupported` + a demux probe. MP4 is transmuxed to fMP4
  in-page (no ffmpeg); WebM shares as the `WEBM` profile **only if every
  peer lists the WebM codec string in `Caps.codecs`**
  ([media-pipeline.md](media-pipeline.md) §3.3).
- **Storage**: sliding-window partial seeder (hot RAM + OPFS pack files),
  [storage.md](storage.md) §2. Call `navigator.storage.persist()` and
  handle quota failures.
- **Identity**: Ed25519 + X25519 keys in IndexedDB, accessed only from the
  Worker. Strict CSP; XSS is identity theft.
- **Upload**: allowed and default-on, `LOW_UPLOAD` capability flag for
  metered connections throttles it.
- **WebRTC**: only on the main thread; the Worker receives block bytes via
  transferables and sends requests/acks back. At 8–20 Mbps this is
  manageable; at 100+ Mbps use transferables and avoid per-block copies.

### 2.4 Browser support matrix (v1 target)

| Feature | Chrome/Edge | Firefox | Safari | WebKitGTK |
|---------|-------------|---------|--------|-----------|
| WebRTC DataChannels | ✓ | ✓ | ✓ | ✓ |
| `RTCPeerConnection` in Worker | ✗/experimental | ✗ | ✗ | ✗ |
| MSE fMP4 H.264/AAC | ✓ | ✓ (platform codecs) | ✓ | ✓ with GStreamer plugins |
| MSE WebM/VP9/Opus | ✓ | ✓ | partial/✗ | ✓ with plugins |
| OPFS sync handles in Worker | ✓ | ✓ | ✓ (recent) | ✓ (recent) |

The browser app must feature-detect, not assume. If MSE or OPFS is missing,
degrade to a warning / streaming-only mode rather than failing silently.

---

## 3. Desktop (Tauri 2, Linux)

### 3.1 Architecture

```
Rust core process (native pw-core + webrtc-rs + pw-media + pw-store)
   ├─ Tauri window (WebKitGTK webview) running the shared web UI
   │    ├─ control RPC (small JSON/typed messages)
   │    └─ media stream: local HTTP/custom protocol serving verified
   │       fMP4 segments from the native store to MSE
   └─ optional mpv fallback: external window + JSON-RPC IPC +
      localhost range server
```

The frontend cannot be *identical* to the browser backend by construction:
in the browser the Worker owns pieces and feeds MSE; on the desktop the Rust
core owns pieces and the webview owns MSE. Pretending otherwise led to an
unworkable “send megabytes over Tauri JSON IPC” design. The bridge is split:

- **Control RPC**: small messages (state, votes, chat, play/pause/seek,
  seek completion). Tauri IPC is fine here.
- **Media stream**: a local HTTP endpoint or Tauri custom protocol. The
  webview fetches verified segments with `fetch`/`ReadableStream` and
  appends them to MSE. The endpoint is bound to `127.0.0.1`, uses a random
  session token, and checks `Origin`/`Sec-Fetch-Site` where available. It
  never serves unverified bytes.

This keeps the native scheduler/store in Rust and the UI/MSE in the
webview without a JSON-media path.

### 3.2 Player selection (decision log, [roadmap.md](../roadmap.md))

At startup the app runs capability detection:

```
if MediaSource.isTypeSupported('video/mp4; codecs="avc1.640028,mp4a.40.2"'):
    → INLINE MSE (default): video plays inside the app window with the full
      integrated UI (chat overlay, vote toasts, room seek bar, free-roam).
else:
    → MPV FALLBACK: spawn mpv as an external window fed by the localhost
      range server. The Tauri window keeps every control (play/pause/
      seek/votes/chat) and drives mpv over JSON-RPC IPC.
```

Why not mpv-always: (1) embedding libmpv into a WebKitGTK surface means raw
X11/Wayland plumbing with no mature plugin, (2) inline MSE gives the
integrated UI that a desktop app should have, (3) the browser needs the MSE
player anyway, so mpv-always would buy nothing but divergence.

**Why the fallback exists at all**: WebKitGTK’s MSE is GStreamer-based;
H.264/AAC decode needs `gst-plugins-{good,bad,ugly}` (+ openh264/x264).
Present on most desktop distros; the Flatpak/AppImage bundles the plugins so
the fallback is rare. When it triggers, the app tells the user exactly which
packages to install to get inline playback.

### 3.3 Desktop extras

- ffmpeg discovery: bundled sidecar first, `PATH` second (clear error if
  absent; needed only when *this peer* proposes a non-compatible file).
- Native storage backend (append-only file + journal,
  [storage.md](storage.md) §1).
- Desktop notifications for votes/proposals; media keys map to *local*
  free-roam controls (never to room transport).
- Native mpv playback is sandboxed where possible (restricted protocol
  whitelist, no network, bubblewrap/firejail). See
  [security.md](security.md) §5.

### 3.4 Packaging/licensing caveat

Bundling GStreamer codec plugins and ffmpeg in Flatpak/AppImage has patent
and license implications (H.264/AAC). Ship a working configuration and
document it; do not assume “it’s in the distro” for every user.

---

## 4. CLI (Linux)

### 4.1 Process model: daemon + control client

The CLI is **two processes**, not a set of independent one-shot commands:

```
pw-daemon   # long-running peer: transport, store, core, mpv, TUI
pw          # thin control client over a Unix socket
```

- `pw-daemon` owns the room state, keys, store, and player.
- `pw` subcommands (`join`, `propose`, `vote`, `peers`, `status`, `leave`)
  connect to `$XDG_RUNTIME_DIR/peerwatch/<pid>.sock` (mode `0600`) and
  send control requests.
- This is what makes `pw propose <file>` work while a room is already
  running. Independent one-shot commands would each need their own copy of
  the room state, which is not implementable.
- The daemon can run with `--no-tui` for scripts; all interactivity also
  exists as control requests for automation.
- `pw signal serve` runs the relay as a separate process/crate
  (`pw-signal-relay`), not inside a peer.

### 4.2 The mpv adapter

```
pw-daemon ──spawn──► mpv --input-ipc-server=<sock> http://127.0.0.1:<port>/<session-token>
   │                    ▲
   │ JSON-RPC over unix socket (play/pause/seek/speed/get time)
   ▼                    │ HTTP Range requests
localhost range server ◄┘   ← reads the append-only file
```

- The range server answers `Range:` from the media file, a range that
  crosses missing pieces triggers the scheduler's URGENT class and a short
  stall-retry (mpv's cache absorbs small gaps; the sync layer's
  buffer-awareness handles bigger ones, [sync.md](sync.md) §4).
- Bind to `127.0.0.1`, use a random port and a per-session bearer token in
  the URL, and check `Origin`/`Host`. A bare localhost server is readable by
  any local process.
- `speed` property = PI controller actuator, `time-pos` polling at 2 Hz
  feeds the controller's `x_local`. Prefer `observe_property` if available
  to reduce polling jitter.
- mpv runs with a restricted protocol whitelist and no network access to
  reduce the malicious-media attack surface.

### 4.3 Terminal UI

`ratatui` interface alongside the mpv window: roster with peer IDs, current
proposal/vote/certificate status with keybinds to vote (`y`/`n`), free-roam
indicator + `L` = return to live, per-peer throughput, buffer depth, chat.
Pure-terminal mode (`--no-tui`) prints events.

### 4.4 CLI commands (sketch)

```
pw daemon [--no-tui]                    # start the long-running peer
pw room create --invites 3              # create room + print invite tokens
pw join <invite-token>                  # join through the daemon
pw propose <file>                       # probe → pipeline → broadcast proposal
pw vote <proposal-id> <y|n>             # (usually via TUI prompt)
pw peers                                # roster, throughputs, scores
pw status                               # room/stream/term/host/buffer
pw signal serve [--port 7878]           # run the signaling relay (self-host)
```

---

## 5. Interop matrix

| | Browser | Desktop | CLI |
|---|---|---|---|
| Browser | — | full mesh ✓ | full mesh ✓ |
| Desktop | ✓ | — | ✓ |
| CLI | ✓ | ✓ | — |

All pairs speak ICE/DTLS/SCTP + the v2 protocol identically. The real
platform asymmetries are:

| Asymmetry | Browser | Desktop | CLI |
|-----------|---------|---------|-----|
| Storage | sliding window + OPFS | append-only file/cache | append-only file/cache |
| Proposable files | mp4/webm | anything ffmpeg reads | anything ffmpeg reads |
| Playback | MSE (main thread) | MSE default / mpv fallback | mpv |
| Codec support | browser-dependent | GStreamer + mpv | mpv |
| Network | main-thread WebRTC | native webrtc-rs | native webrtc-rs |
| Identity storage | IndexedDB (XSS-exposed) | file `0600` | file `0600` |
| Background behavior | tab throttling | normal | normal |

Mixed rooms are the norm, not the exception. Capability flags
([protocol.md](../spec/protocol.md) §2.1) are what make the
asymmetries explicit instead of surprising.
