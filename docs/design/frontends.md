---
tier: design
---

# Frontends — Browser App & Native Daemon

PeerWatch v1 ships **two frontends** powered by one shared `pw-core` and one
shared `web/` UI. Native desktop packaging (Tauri) is deferred to v2 as a thin
wrapper over the native daemon API.

---

## 1. The player boundary

`pw-core` drives playback via commands and events ([core-api.md](../spec/core-api.md) §4):

```rust
pub enum PlayerCommand {
    Load { stream_id: StreamId, mime: String, init_segment: Bytes, subtitles: Vec<SubtitleTrack> },
    Play, Pause, Seek { t_ms: u64 }, SetRate { rate: f32 },
    AppendSegment { seg: u32, bytes: Bytes, token: AppendToken },
    Flush, Evict { up_to_ms: u64 },
}

pub enum PlayerEvent {
    Position { t_ms: u64 },                 // >= 10 Hz (20 Hz recommended)
    BufferedAhead { ms: u64 },
    State { playing: bool, buffering: bool, ended: bool },
    SeekComplete { t_ms: u64 },
    AppendDone { token: AppendToken },
    AppendRejected { token: AppendToken, reason: AppendReject },
    Error { code: ErrorCode, message: String },
}
```

### 1.1 MSE backpressure (`AppendToken` + `writable_bytes`)

MSE `SourceBuffer.appendBuffer()` is asynchronous (`updateend`) and throws
`QuotaExceededError` when full. `pw-core` only issues `AppendSegment` while the
adapter reports sufficient `writable_bytes()`, tracks one completion event per
`AppendToken`, and responds to `QuotaExceeded` by issuing `Evict { up_to_ms }`
and retrying once — never dropping the stream.

---

## 2. Frontend 1: Browser App (`web/` + `pw-wasm`)

Zero-install peer running entirely inside a modern browser tab (Chrome, Edge,
Firefox, Safari):

```
Main thread:   web/ UI (DOM, <video>, MSE SourceBuffer, WebVTT <track>s)
               RTCPeerConnection + RTCDataChannel (aggregate bufferedAmount monitor)
               live MediaSource.isTypeSupported() codec probe
               JS transmux adapter (mp4box.js / mediabunny for browser-proposed MP4/WebM)
               autoplay gesture + Page Visibility (AWAY on background tab)

Web Worker:    pw-core (WASM via pw-wasm)
               Ed25519 / X25519 / BLAKE3 crypto + Worker-only IndexedDB identity
               RAM hot tier + OPFS synchronous pack files (sliding-window partial seeder)

Bridge:        MessagePort with transferable ArrayBuffers (zero-copy block/segment passing)
```

Browser capabilities & limits:
- Downloads, verifies, plays via MSE, and uploads from RAM + OPFS.
- Proposes local `.mp4` / `.webm` files via JS transmuxing without ffmpeg.
- Handles `QuotaExceededError` and missing OPFS by degrading to RAM-only.

---

## 3. Frontend 2: Native Daemon (`pw-cli` + `pw-node`, Linux / macOS / Windows)

A single native binary (`peerwatch`) running native `pw-core`, **`str0m`**
WebRTC, native disk segment cache, and `ffmpeg` (remux + HW-accelerated
transcode-ahead + subtitle extraction).

The daemon supports **two playback modes** seamlessly:

### 3.1 Local Web UI mode (default for interactive native users)

```
peerwatch daemon
  └─ binds http://127.0.0.1:<random-port>/?token=<random-secret>
       ├─ serves the exact same web/ UI into the user's real browser
       ├─ WebSocket /ws/control  (UiCommand / UiEvent JSON/CBOR)
       └─ HTTP /media/<stream>/<seg>  (serves verified fMP4 segments & WebVTT to MSE)
```

Why this is the default native experience in v1:
- **Zero WebKitGTK / GStreamer codec headaches**: the user's existing Chrome,
  Firefox, or Safari browser already ships hardware-accelerated H.264/VP9/AV1
  MSE decoding on Linux, macOS, and Windows.
- **100% shared UI**: chat, votes, collaborative queue, subtitle selector, and
  buffer diagnostics are written once in `web/`.
- **Native superpowers**: full 20 GiB disk cache, `str0m` WebRTC, and any local
  MKV/HEVC/AV1 file via `ffmpeg` remux or VA-API/NVENC/VideoToolbox transcode.

### 3.2 `mpv` + CLI / optional TUI mode

For power users, headless seedboxes, or tiling-WM workflows:

```sh
peerwatch daemon --player mpv [--tui | --no-tui]
peerwatch room create --mode collaborative --invites 3
peerwatch join <pw3_invite_token>
peerwatch propose movie.mkv --audio jpn --sub eng
peerwatch queue add ep02.mkv
peerwatch status
```

- `pw-node` spawns `mpv --input-ipc-server=<sock>` pointing at a tokenized
  `127.0.0.1` HTTP range server (mapping byte ranges via segment `byte_len`
  prefix sums, checking bearer token & `Host`).
- `mpv` uses `observe_property("time-pos")` (≥ 10–20 Hz feedback) and runs
  with a restricted protocol whitelist and `bubblewrap`/`firejail` sandboxing
  where available.

---

## 4. Platform comparison

| Capability | Browser App (`pw-wasm`) | Native Daemon (`peerwatch`) |
|---|---|---|
| Install | None (URL) | Single binary |
| WebRTC | `RTCPeerConnection` (main thread) | `str0m` (Sans-I/O native) |
| Playback | Inline MSE | Real browser MSE (default) or `mpv` |
| Storage | RAM hot + OPFS pack files (sliding window) | RAM hot + disk pack files (20 GiB LRU) |
| Local proposals | MP4 / WebM (JS transmux) | Any container/codec (`ffmpeg` remux / HW transcode + subtitles) |
| Platforms | Chrome, Firefox, Safari | Linux, macOS, Windows |
| Desktop shell | — | Optional Tauri wrapper in **v2** |
