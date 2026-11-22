---
tier: design
---

# Performance Model

Quantitative targets and budgets for PeerWatch v3.

**Core constants**: block = **32 KiB** · segment = **≈ 4 s** (keyframe-aligned,
init = seg 0) · playback buffer target = **60 s** · URGENT window = **10 s** ·
default envelope = **64 KiB** · aggregate SCTP backpressure watermarks =
**128 / 256 KiB**.

---

## 1. Stream demand & manifest sizing

| Quality | Bitrate | 4 s Segment | Blocks/seg | 2 h Segments | Manifest total (2 h) | Merkle overhead / 32 KiB block |
|---|---:|---:|---:|---:|---:|---:|
| SD | 2 Mbps | 1 MB | 32 | ~1,800 | ~100 KiB (~4 chunks) | 5 hashes (160 B = 0.49%) |
| 1080p | 8 Mbps | 4 MB | 128 | ~1,800 | ~100 KiB (~4 chunks) | 7 hashes (224 B = 0.68%) |
| 4K | 20 Mbps | 10 MB | 320 | ~1,800 | ~100 KiB (~4 chunks) | 9 hashes (288 B = 0.88%) |

Total data-plane framing + Merkle overhead stays around **~1.0–1.5%**, while
manifest transfer drops to **~4 chunks (~100 KiB)** even for a 100 GB 4K file.

---

## 2. Supply, fan-out, and BDP pipelining

- **Host uplink de-bottlenecking**: because verified 32 KiB blocks in RAM are
  advertised immediately via `AVAILABILITY.frontier` (no disk `fdatasync`
  stall and no waiting for a full 4 s segment), a receiving peer begins
  re-uploading a block within ~50–100 ms of receiving it. Combined with
  rarest-first backfill, Host fan-out scales rapidly across the mesh.
- **Adaptive pipeline depth**: `Q = clamp(⌈θ̂ · RTT̂ / 32 KiB⌉, 4, 64)`:
  - LAN (1 Gbps × 0.5 ms) → `Q = 4`
  - Broadband (20 Mbps × 30 ms) → `Q = 4`
  - Fast WAN (100 Mbps × 100 ms) → `Q ≈ 40`
- **Aggregate SCTP pacing**: pausing `Data`/`Bulk` sends above 256 KiB total
  `bufferedAmount` keeps `control` RTT flat (< 20 ms queueing delay) even under
  saturated uploads.

---

## 3. Cold start (invite paste → first frame)

| Step | Typical latency |
|---|---|
| Invite exchange + stateless relay routing | 100–250 ms |
| ICE + DTLS (`str0m` ↔ WebRTC) | 0.5–2.5 s |
| `HELLO`/`AUTH` + `STATE_DIGEST`/`STATE_SYNC` | 1–2 RTTs (~100 ms) |
| `MANIFEST_HEAD` + ~4 chunks (~100 KiB) | 100–250 ms |
| Segment `0` (init) + 2 media segments (8 MB @ 1080p) | 2–6 s |
| **Total** | **~3–9 s** |

Live transcode-ahead adds ~12–15 s head start on the first stream; remux starts
almost immediately.

---

## 4. Control traffic (16-peer full mesh, 2 h film)

| Message | Rate | Typical size | Originated room-wide |
|---|---:|---:|---:|
| `AVAILABILITY` (raw/RLE + frontier) | 1/s/peer | 8–260 B | ~2–3 KB/s |
| `SYNC` (Host) | 1/s | ~80 B | ~1.3 KB/s |
| `PING`/`PONG` | 0.2/s/conn | ~40 B | ~2 KB/s |
| `STATE_DIGEST` / votes / queue | on change | 0.1–2 KB | negligible |

Full-mesh control wire overhead at 16 peers is **~35–50 KB/s** total (< 0.5% of
an 8 Mbps stream).

---

## 5. CPU & memory budgets

- **Watching peer (1080p, 8 peers)**: BLAKE3 (< 1% core), Ed25519 (< 1 ms/s),
  `str0m` DTLS/SCTP (~2–4% core), HW video decode (~0–5% core) → **< 15% of
  one core**.
- **Hosting peer (remux)**: + < 5% CPU (I/O-bound).
- **Hosting peer (transcode-ahead)**:
  - **HW encoder** (VA-API / NVENC / VideoToolbox / QSV): **< 10% CPU**, 5–10×
    realtime.
  - **Software fallback** (`libx264 veryfast`): 30–70% CPU, 2–5× realtime.
- **Browser memory**: ~128–256 MiB RAM hot tier + 60–150 MiB MSE `SourceBuffer`
  + WASM heap (~300–500 MiB total at 1080p–4K).

---

## 6. Sync accuracy (retuned PI controller)

| Parameter | Value |
|---|---|
| Gains | `Kp = 0.30`, `Ki = 0.0225` (`ζ = 1`, `τ ≈ 6.67 s`) |
| 50% step reduction time | **~11.2 s** |
| Playback rate clamp | **±5%** (`[0.95, 1.05]`, imperceptible pitch-preserved) |
| Hard-seek threshold / anti-windup band | **2.0 s** / **1.5 s** |
| Steady-state inter-peer skew | **→ 0** (floor = RTT asymmetry, ~10–30 ms; perceptual target < 100 ms) |
