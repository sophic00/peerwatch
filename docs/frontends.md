# Frontends — CLI, Desktop, Browser

Three ways to be a peer. All run the same `pw-core`, they differ in **how
video is played**, **how UI is rendered**, and **what the platform allows**.
This document is the per-platform handbook.

---

## 1. The player abstraction

```rust
trait Player {
    async fn load(&self, content: &Manifest, store: Arc<dyn PieceStore>);
    async fn play(&self); async fn pause(&self);
    async fn seek(&self, t_ms: u64);
    async fn set_rate(&self, r: f32);          // 0.9 – 1.1 (PI controller range)
    async fn position(&self) -> u64;           // ms
    async fn buffered_ahead(&self) -> u64;     // ms beyond playhead
}
```

The sync layer drives this trait, the trait hides which of the three
implementations is underneath.

| Frontend | Player impl | UI |
|----------|------------|----|
| Browser | **MSE player** (web UI) | web UI (full) |
| Desktop (Tauri) | **MSE player** in the webview *(default)*, **mpv adapter** *(fallback)* | same web UI in webview |
| CLI | **mpv adapter** | terminal (ratatui) + mpv window |

---

## 2. Browser

### 2.1 Architecture

```
main thread:   web UI (DOM, <video>, MSE SourceBuffers)
web worker:    pw-core (WASM) — scheduler, store, sync, governance
               + OPFS sync file access + WebRTC (RTCPeerConnection)
bridge:        postMessage RPC (typed, generated)
```

Running the core in a **worker** is load-bearing: SHA-256 verification and
scheduler ticks must never block (or be blocked by) the UI, and OPFS sync
handles only exist in workers ([storage.md](storage.md)).

### 2.2 The MSE player

1. On `MANIFEST`: `new MediaSource()`, `addSourceBuffer(manifest.codecs)`
   (e.g. `video/mp4; codecs="avc1.640028,mp4a.40.2"`).
2. Append the **init segment** as soon as its pieces verify.
3. Append media segments in decode order as their pieces complete, keep
   30–60 s appended ahead of the playhead, evict behind
   (`SourceBuffer.remove`) on memory pressure.
4. **Seek**: `remove()` the buffered ranges around the target, jump the
   scheduler's window to the seek segment
   ([distribution.md](distribution.md)), append from the segment start
   (keyframe).
5. Rate control = `video.playbackRate` (the PI controller's actuator,
   [sync.md](sync.md)), position = `video.currentTime`, buffer health =
   `video.buffered`.

### 2.3 Browser-specific limits

- **Proposals**: mp4/webm only, validated by
  `MediaSource.isTypeSupported` + a demux probe, MP4 is transmuxed to fMP4
  in-page (no ffmpeg), WebM shares as the `WEBM` profile
  ([media-pipeline.md](media-pipeline.md) §3.3).
- **Storage**: sliding-window partial seeder (hot RAM + OPFS),
  [storage.md](storage.md) §2.
- **Identity**: Ed25519 key in IndexedDB ([identity.md](identity.md)).
- **Upload**: allowed and default-on, `LOW_UPLOAD` capability flag for
  metered connections throttles it.

## 3. Desktop (Tauri 2, Linux)

### 3.1 Architecture

```
Rust core process (native pw-core + webrtc-rs + pw-media)
   └─ Tauri window (WebKitGTK webview) running THE SAME web UI as the browser
        └─ IPC bridge (mirrors the browser's worker RPC API)
```

The frontend cannot tell whether its backend is a WASM worker or the Tauri
Rust process. The bridge API is identical by construction.

### 3.2 Player selection (decision log, [roadmap.md](roadmap.md))

At startup the app runs capability detection:

```
if MediaSource.isTypeSupported('video/mp4; codecs="avc1.640028,mp4a.40.2"'):
    → INLINE MSE (default): video plays inside the app window with the full
      integrated UI (chat overlay, vote toasts, room seek bar, free-roam).
else:
    → MPV FALLBACK: spawn mpv as an external window fed by the localhost
      range server (§4.1). The Tauri window keeps every control (play/pause/
      seek/votes/chat) and drives mpv over JSON-RPC IPC.
```

Why not mpv-always: (1) embedding libmpv into a WebKitGTK surface means raw
X11/Wayland plumbing with no mature plugin, (2) inline MSE gives the
integrated UI that a desktop app should have, (3) the browser needs the MSE
player anyway, so mpv-always would buy nothing but divergence.

**Why the fallback exists at all**: WebKitGTK's MSE is GStreamer-based,
H.264/AAC decode needs `gst-plugins-{good,bad,ugly}` (+ openh264/x264).
Present on most desktop distros, the Flatpak/AppImage bundles the plugins so
the fallback is rare. When it triggers, the app tells the user exactly which
packages to install to get inline playback.

### 3.3 Desktop extras

- ffmpeg discovery: bundled sidecar first, `PATH` second (clear error if
  absent. Needed only when *this peer* proposes a non-compatible file).
- Native storage backend (sparse file, [storage.md](storage.md) §1).
- Desktop notifications for votes/proposals, media keys map to *local*
  free-roam controls (never to room transport).

## 4. CLI (Linux)

### 4.1 The mpv adapter (evolved from v1)

```
pw-cli ──spawn──► mpv --input-ipc-server=<sock> http://127.0.0.1:<port>/
   │                    ▲
   │ JSON-RPC over unix socket (play/pause/seek/speed/get time)
   ▼                    │ HTTP Range requests
localhost range server ◄┘   ← reads sparse file directly ([storage.md] §1)
```

- The range server answers `Range:` from the sparse partial file, a range
  that crosses missing pieces triggers the scheduler's URGENT class and a
  short stall-retry (mpv's cache absorbs small gaps, the sync layer's
  buffer-awareness handles bigger ones,
  [sync.md](sync.md) §4).
- `speed` property = PI controller actuator, `time-pos` polling at 2 Hz
  feeds the controller's `x_local`.

### 4.2 Terminal UI

`ratatui` interface alongside the mpv window: roster with peer IDs, current
proposal/vote status with keybinds to vote (`y`/`n`), free-roam indicator +
`L` = return to live, per-peer throughput, buffer depth, chat. Pure-terminal
fallback (`--no-tui`) prints events, all interactivity also exists as
subcommands for scriptability.

### 4.3 CLI commands (sketch)

```
pw room create                          # print invite token, wait
pw join <token>                         # join; auto-play current video if any
pw propose <file>                       # probe → pipeline → broadcast proposal
pw vote <proposal-id> <y|n>             # (usually via TUI prompt)
pw peers                                # roster, throughputs, scores
pw signal serve [--port 7878]           # run the signaling relay (self-host)
```

## 5. Interop matrix

| | Browser | Desktop | CLI |
|---|---|---|---|
| Browser | — | full mesh ✓ | full mesh ✓ |
| Desktop | ✓ | — | ✓ |
| CLI | ✓ | ✓ | — |

All pairs speak ICE/DTLS/SCTP + the v2 protocol identically. The only
platform *asymmetries* are storage budget (advertised via capability flags)
and which files each can *propose* (browser: mp4/webm, native: anything
ffmpeg reads). Mixed rooms are the norm, not the exception.
