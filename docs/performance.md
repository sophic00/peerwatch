# Performance Model — Numbers Behind the Design

Every formula and budget that shaped the protocol, collected in one place.
Use it to sanity-check changes: if you alter a constant (piece size, buffer
targets, Q limits), re-derive the affected rows.

**Constants:** piece = 512 KiB = 4.19 Mbit · block = 64 KiB = 0.52 Mbit ·
segment = 4 s of video · playback buffer target = 60 s · URGENT window = 10 s.

---

## 1. Demand: what playback costs

| Quality | Bitrate | Segment | Pieces/segment | Sustained piece demand |
|---------|--------:|--------:|---------------:|-----------------------:|
| SD | 2 Mbps | 1 MB | 2 | 0.5 /s |
| 1080p | 8 Mbps | 4 MB | 8 | 2 /s |
| 4K/high | 20 Mbps | 10 MB | 20 | 5 /s |

A peer must fetch the stream **once**: mean download ≥ video bitrate, with
the 60 s buffer absorbing variance. Rule of thumb: stall-free requires mean
effective rate ≥ **1.2× bitrate**.

## 2. Supply: how fast a piece reaches a peer

Block transfer time: `T_block ≈ RTT + block_bits / B_eff`, where
`B_eff = min(sender_free_upload, receiver_free_download)`. Pieces are
pipelined (BDP-sized in-flight windows, [distribution.md](distribution.md)
§4.3), so `T_piece ≈ 4.19 Mbit / B_eff` once saturated.

| Scenario | B_eff | RTT | T_piece | vs 2 piece/s (1080p) |
|----------|------:|----:|--------:|----------------------|
| LAN | 1 Gbps | 0.5 ms | ~5 ms | 400× headroom |
| Host → one peer (20 Mbps up) | 20 Mbps | 30 ms | ~0.24 s | 12× headroom |
| Same host, uplink split by 8 peers | 2.5 Mbps | 30 ms | ~1.7 s | marginal ⚠ |
| **Swarm striping**: blocks from 4 peers @ 2 Mbps | 8 Mbps agg. | 30 ms | ~0.52 s | good ✓ |
| Rare piece: only host has it, uplink /10 | 2 Mbps | 30 ms | ~2.1 s | buffer absorbs |
| Lossy path: 2 % loss, 200 ms RTT | ≈0.35 Mbps (Mathis: `(MSS/RTT)·(1/√p)`) | 200 ms | ~12 s | route around ✗ |

**Design consequences:**

1. **The Host's uplink is the early bottleneck.** A piece existing only at
   the Host replicates room-wide at `host_up / K`. Rarest-first backfill
   turns each replicated piece into a *new source* (capacity doubles per new
   holder). This is why backfill isn't optional polish but the mechanism
   that de-bottlenecks the Host. With 20 Mbps up and 8 Mbps video, the Host
   injects at 2.5× realtime: the swarm reaches full redundancy while
   watching, with the 60 s buffer covering the early deficit.
2. **Lossy/high-RTT paths collapse ∝ 1/√p.** Never depend on one source:
   capacity-proportional striping + hedged re-requests
   ([distribution.md](distribution.md) §4.4) exist precisely for this row.
3. **In-flight bytes must cover BDP** (`Q = ⌈θ̂·RTT̂ / 64KiB⌉`, clamp
   [4, 64]). Fixed Q=8 would cap a 100 Mbps×100 ms path at 41 Mbps.

## 3. Cold start: token-paste → first frame

| Step | Cost |
|------|------|
| Signaling WS connect + roster | 50–200 ms |
| ICE: STUN (~100 ms) + checks + DTLS (2 RTTs) | 0.5–3 s |
| AUTH + ROOM_STATE + MANIFEST | ~1–2 RTTs (~100 ms) |
| Init segment + 2 segments buffered (8 MB @ ~8–16 Mbps effective) | 2–8 s |
| **Total** | **≈ 4–10 s** (comparable to joining a video call) |

Mid-video join is faster (buffering gate starts from the live frontier, not
t=0). Proposals with remux pipelines add ~0 setup, transcode-ahead adds the
~15 s encoder head start ([media-pipeline.md](media-pipeline.md) §3.2).

## 4. CPU budget (1080p room, 8 peers, native peer)

| Component | Cost |
|-----------|------|
| SHA-256 verify, 2 piece/s | < 1 % of one core (SHA-NI ≈ 1 GB/s/core) |
| Ed25519 sign/verify (~tens/s) | ~70 µs/verify → < 1 ms/s, negligible |
| DTLS-SCTP stack @ ~30 Mbps data plane | ~3–5 % of one core (≈1 % per 10 Mbps) |
| H.264 decode: hardware (VA-API) / software | ~0 / 15–25 % of one core |
| Scheduler + sync + UI | < 2 % |
| **Watching peer** | **< ½ core total (HW decode)** |
| Host, remux source | + < 5 % (I/O-bound) |
| Host, **transcode-ahead** source | x264 veryfast 1080p ≈ 2–5× realtime on 4–8 cores → **30–70 % of the machine while active** |

The transcode row is the only heavy line. The price of "any format →
browser-compatible", and the reason remux-when-possible matters.

## 5. Memory / storage budgets

| Where | Budget |
|-------|--------|
| Browser hot tier (RAM) | ≤ 256 MiB (≈ ±2 min @ 1080p) |
| Browser warm tier (OPFS) | ≤ 50 % of `storage.estimate()` quota |
| Native cache | full videos, global cap 20 GiB LRU (config) |
| Piece-assembly buffers | ≤ Q×64 KiB per peer + 8 pieces reassembly |

## 6. Control-plane traffic (16-peer room, 6 GB video)

| Flow | Rate | Size | Total |
|------|-----:|-----:|------:|
| BITFIELD | 1/s/peer | ~0.3–0.5 KB (Roaring, [roaring-compression.md](roaring-compression.md)) | ~5 KB/s room-wide (was ~24 KB/s raw) |
| SYNC | 1/s (host) | ~60 B | ~1 KB/s |
| PING/PONG | 0.2/s/conn | ~40 B | ~2 KB/s |
| Votes/proposals | rare | ~200 B | noise |

**Conclusion: control traffic is still three orders of magnitude below the data
plane (Mbps). The bitfield is the one control message worth compressing
(Roaring, [roaring-compression.md](roaring-compression.md)); the rest stay
uncompressed for clarity.**

## 7. Seek cost accounting

On seek: scheduler intersects in-flight requests with the new window
(re-prioritizes), cancels the rest of the *queues*, and the player flushes
MSE ranges / mpv cache. Costs:

| Term | Size |
|------|------|
| In-flight "waste" per connection (unavoidable, stored anyway) | ≤ BDP (31 KB @ 5 Mbps·50 ms, 1.25 MB @ 100 Mbps·100 ms worst) |
| Saved by CANCEL (queued, would-have-sent) | 2–4 MB/peer → 20–40 MB @ 10 peers |
| Post-seek stall (fetch new window urgent class) | ~1–3 s typical broadband |
| **Without CANCEL: added stall from queue drain** | up to ~8 s on 2 Mbps paths |

## 8. Sync accuracy (PI controller, [sync.md](sync.md))

| Term | Value |
|------|-------|
| Steady-state inter-peer skew | → 0 (integral action), floor = RTT-estimation noise ≈ 10–50 ms |
| v1 deadband standing error (for comparison) | up to ±500 ms |
| Correction rate deviation | ≤ ~4 %, exponentially decaying, inaudible |
| Hard-seek threshold / anti-windup band | 5 s / 2 s |
| Gains | Kp = 0.04, Ki = 0.0004 (ζ = 1, τ ≈ 25 s) |

Perceptual reference: inter-peer disagreement < 100 ms is "simultaneous"
for group watching, the design target floor (~50 ms) is 2–10× better than
the threshold, with zero standing error, unlike the tiered scheme.

## 9. Scale envelope

| Peers | Connections/client | Verdict |
|------:|-------------------:|---------|
| ≤ 8 | ≤ 7 | comfortable everywhere |
| 16 | 15 | target ceiling: browsers OK, control traffic trivial, host uplink the real constraint |
| > 16 | > 15 | works but untested, partial-mesh overlay is the designed escape ([roadmap.md](roadmap.md)) |
