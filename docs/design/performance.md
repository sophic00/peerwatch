---
tier: design
---

# Performance Model — Numbers Behind the Design

Every formula and budget that shaped the protocol, collected in one place.
Use it to sanity-check changes: if you alter a constant (block/piece size,
buffer targets, Q limits), re-derive the affected rows.

**Constants:** block = 32 KiB = 0.262 Mbit · piece = 512 KiB = 16 blocks =
4.19 Mbit · segment = 4 s of video · playback buffer target = 60 s ·
URGENT window = 10 s · default DataChannel envelope = 64 KiB.

The 32 KiB block is deliberate: it fits inside a 64 KiB DataChannel message
with a 4-hash Merkle proof and envelope metadata, with margin
([design-log.md](design-log.md) D9).

---

## 1. Demand: what playback costs

| Quality | Bitrate | Segment | Pieces/segment | Blocks/segment | Sustained piece demand |
|---------|--------:|--------:|---------------:|---------------:|-----------------------:|
| SD | 2 Mbps | 1 MB | 2 | 32 | 0.5 /s |
| 1080p | 8 Mbps | 4 MB | 8 | 128 | 2 /s |
| 4K/high | 20 Mbps | 10 MB | 20 | 320 | 5 /s |

A peer must fetch the stream **once**: mean download ≥ video bitrate, with
the 60 s buffer absorbing variance. Rule of thumb: stall-free requires mean
effective rate ≥ **1.2× bitrate**.

---

## 2. Supply: how fast a block/piece reaches a peer

Block transfer time: `T_block ≈ RTT + block_bits / B_eff`, where
`B_eff = min(sender_free_upload, receiver_free_download)`. Blocks are
pipelined (BDP-sized in-flight windows, [distribution.md](distribution.md)
§4.3), so `T_piece ≈ 4.19 Mbit / B_eff` once saturated.

| Scenario | B_eff | RTT | T_piece | vs 2 piece/s (1080p) |
|----------|------:|----:|--------:|----------------------|
| LAN | 1 Gbps | 0.5 ms | ~5 ms | 400× headroom |
| Host → one peer (20 Mbps up) | 20 Mbps | 30 ms | ~0.21 s | 14× headroom |
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
3. **In-flight bytes must cover BDP** (`Q = ⌈θ̂·RTT̂ / 32 KiB⌉`, clamp
   [4, 64]). Examples:
   - 1 Gbps × 0.5 ms → BDP ~62 KB → Q=4 (floor).
   - 20 Mbps × 30 ms → BDP ~75 KB → Q=4 (floor).
   - 100 Mbps × 100 ms → BDP ~1.25 MB → Q≈40.
   A fixed Q=8 would cap the last path at ~20 Mbps.
4. **SCTP backpressure is real.** `Q` is an app-level target; the scheduler
   must also stop when `bufferedAmount` is above its low threshold. Queueing
   beyond the transport only adds latency.

### 2.1 Overhead of integrity

| Item | Cost | Relative to content |
|------|------|---------------------|
| Block hash + 4-sibling Merkle proof | 32 B hash + 128 B proof per 32 KiB block | ~0.49 % |
| Piece root (32 B per 512 KiB piece) | 32 B / 512 KiB | ~0.006 % |
| Envelope / CBOR / signatures | ~100–300 B per message | ~0.3–0.9 % |
| **Total data-plane overhead** | | **~1–2 %** |

This is the price of per-block attribution. It is a good trade: the old
piece-level hash could not identify a poisoner, and false bans were worse
than 1 % overhead.

---

## 3. Cold start: token-paste → first frame

| Step | Cost |
|------|------|
| Invite join + signaling WS + roster | 100–300 ms |
| ICE: STUN (~100 ms) + checks + DTLS (2 RTTs) | 0.5–3 s |
| AUTH + ROOM_STATE + HOST_LEASE | ~1–2 RTTs (~100 ms) |
| MANIFEST_HEAD + needed chunks (~400 KB for 6 GB, chunked) | 0.4–1 s at 8 Mbps |
| Init segment + 2 segments buffered (8 MB @ ~8–16 Mbps effective) | 2–8 s |
| **Total** | **≈ 4–10 s** (comparable to joining a video call) |

Mid-video join is faster (the buffering gate starts from the live frontier
and fetches only the manifest chunks around the playhead). Proposals with
remux pipelines add roughly the remux time for a large file; transcode-ahead
adds the ~15 s encoder head start ([media-pipeline.md](media-pipeline.md)
§3.2).

---

## 4. CPU budget (1080p room, 8 peers, native peer)

| Component | Cost |
|-----------|------|
| BLAKE3 block verify + Merkle proof, 2 pieces/s | < 1 % of one core (BLAKE3 ≈ 1–3 GB/s/core) |
| Ed25519 sign/verify (~tens/s) | ~70 µs/verify → < 1 ms/s, negligible |
| DTLS-SCTP stack @ ~30 Mbps data plane | ~3–5 % of one core (≈1 % per 10 Mbps) |
| H.264 decode: hardware (VA-API) / software | ~0 / 15–25 % of one core |
| Scheduler + sync + UI | < 2 % |
| **Watching peer** | **< ½ core total (HW decode)** |
| Host, remux source | + < 5 % (I/O-bound) |
| Host, **transcode-ahead** source | x264 veryfast 1080p ≈ 2–5× realtime on 4–8 cores → **30–70 % of the machine while active** |

The transcode row is the only heavy line. The price of “any format →
browser-compatible”, and the reason remux-when-possible matters.

---

## 5. Memory / storage budgets

| Where | Budget |
|-------|--------|
| Browser hot tier (RAM) | ≤ 256 MiB (≈ ±2 min @ 1080p) |
| Browser warm tier (OPFS pack files) | ≤ 50 % of `storage.estimate().quota`, capped |
| Native cache | full streams if they fit, global cap 20 GiB LRU (config) |
| Block assembly | ≤ Q × 32 KiB per peer + a few 512 KiB piece buffers |
| MSE SourceBuffer | 30–60 s appended ahead (60 MB @ 1080p, 150 MB @ 4K) |
| **Browser total** | hot + MSE + WASM + WebRTC; plan for ~400–600 MB @ 4K |

A 4K browser peer with a 300 MB hot tier, 150 MB MSE buffer, WASM heap, and
15 WebRTC connections can approach 600 MB. Do not advertise 4K support on
memory-constrained devices.

---

## 6. Control-plane traffic (16-peer room, 6 GB stream)

| Flow | Rate | Size | Total |
|------|-----:|-----:|------:|
| BITFIELD | 1/s/peer | ~0.3–0.5 KB (Roaring) | ~5 KB/s room-wide originated |
| SYNC | 1/s (host) | ~80 B | ~1.3 KB/s |
| PING/PONG | 0.2/s/conn | ~40 B | ~2 KB/s |
| Manifest chunks | on join/append | 32 KiB each | bursts, not steady |
| Votes/certs | rare | ~1–4 KB | noise |

At 16 peers, full-mesh wire cost is ~15× originated: roughly **75–100 KB/s
of control traffic**, versus ~1 MB/s for an 8 Mbps data plane. That is
~10–20×, not “three orders of magnitude.” The bitfield is still the one
control message worth compressing; the rest stay uncompressed for clarity.

---

## 7. Seek cost accounting

On seek: the scheduler intersects in-flight requests with the new window
(re-prioritizes), cancels the rest of the *queues*, and the player flushes
MSE ranges / mpv cache. Costs:

| Term | Size |
|------|------|
| In-flight “waste” per connection (unavoidable, stored anyway) | ≤ BDP (31 KB @ 5 Mbps·50 ms, 1.25 MB @ 100 Mbps·100 ms worst) |
| Saved by CANCEL (queued, would-have-sent) | 1–2 MB/peer at 32 KiB blocks → 10–20 MB @ 10 peers |
| Post-seek stall (fetch new window urgent class) | ~1–3 s typical broadband |
| **Without CANCEL: added stall from queue drain** | 4–8 s on 2 Mbps paths (1–2 MB / 2 Mbps) |

The queue-drain figure scales linearly with block size: at 32 KiB it is the
row above; doubling the block size would double it. The ordering rule (cancel
the queue, keep the in-flight) is independent of block size.

---

## 8. Sync accuracy (PI controller, [sync.md](sync.md))

| Term | Value |
|------|-------|
| Steady-state inter-peer skew | → 0 (integral action), floor = RTT-estimation noise ≈ 10–50 ms |
| v1 deadband standing error (for comparison) | up to ±500 ms |
| Correction rate deviation at 1 s error | 8 %, decaying; ≤ 10 % clamp |
| Hard-seek threshold / anti-windup band | 5 s / 2 s |
| Gains | Kp = 0.08, Ki = 0.0016 (ζ = 1, τ = 2/Kp = 25 s) |
| 50 % step reduction (critically damped) | ~42 s, not 17 s |
| P-only skew error for 0.1 % clock offset | 12.5 ms, not 2.5 s |

Perceptual reference: inter-peer disagreement < 100 ms is “simultaneous”
for group watching; the design target floor (~50 ms) is 2–10× better than
the threshold, and the 25 s envelope is fast enough that a viewer never
notices the correction.

---

## 9. Scale envelope

| Peers | Connections/client | Verdict |
|------:|-------------------:|---------|
| ≤ 8 | ≤ 7 | comfortable everywhere |
| 16 | 15 | target ceiling: desktop browsers OK, control traffic trivial, host uplink the real constraint |
| > 16 | > 15 | works but untested; partial-mesh overlay is the designed escape ([roadmap.md](../roadmap.md)) |

Browser connection limits and background-tab throttling are the practical
constraints, not the protocol math. Test 16-peer rooms on the actual
browser matrix before claiming support.
