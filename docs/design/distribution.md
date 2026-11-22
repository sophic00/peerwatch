---
tier: design
---

# Distribution — Segment-Aligned Swarm & Scheduler

This document covers PeerWatch v3's 2-level data plane: how segments are
indexed and verified, how peers advertise completed segments and active
frontier blocks, and how the scheduler picks blocks and peers. Normative wire
formats live in [protocol.md](../spec/protocol.md) §6.

---

## 1. Two-level data model: `Segment → Block`

- **Segment (`0..=N`)**: Segment `0` is the init segment; `1..=N` are ≈ 4 s
  keyframe-aligned media segments. Each segment has exact `byte_len`,
  `t_start_ms`, `t_end_ms`, and a BLAKE3 Merkle root `segment_root`.
- **Block (`0..block_count`)**: ≤ 32 KiB leaf of its segment's Merkle tree.
  Addressed by `(seg, block)`.

Why segment-aligned integrity beats fixed unaligned pieces:

- **Exact byte boundaries**: MSE `appendBuffer(segment_bytes)` requires zero
  box scanning; mpv HTTP range serving maps byte offsets via a simple prefix
  sum of `SegmentEntry.byte_len`.
- **Zero cross-segment coupling**: seeking, evicting, and live transcoding all
  operate on independent segments.
- **Tiny manifests**: a 2-hour film is ~1,800 segments (~100 KiB total manifest,
  ~4 chunks) whether the stream is 2 Mbps SD or 100 GB 4K.
- **Low Merkle overhead**: a 4 MB 1080p segment has 128 blocks → 7 sibling
  hashes (224 B per 32 KiB block = **0.68%**); a 10 MB 4K segment has 320
  blocks → 9 hashes (288 B = **0.88%**).

---

## 2. Advertising availability (`AVAILABILITY`)

Every peer broadcasts `AVAILABILITY` on `control` at **1 Hz**:

1. **`segments`**: compact bitmap of completed segments, encoded as whichever
   of `RAW` or `RLE` is smaller (for a 2-hour movie: **8 bytes** `RLE` for a
   complete seeder, **≤ 226 bytes** `RAW` worst case — no external Roaring
   bitmap dependency).
2. **`frontier`**: up to 8 `{ seg, block_mask }` entries covering in-progress
   segments near the playhead.

Because every 32 KiB block verifies independently against `segment_root`, a
peer advertises verified frontier blocks in RAM **immediately** (sub-second
fan-out from the Host!) without waiting for the entire 4 s segment to finish or
for disk `fdatasync`.

---

## 3. The scheduler

Every scheduling tick (100 ms), `pw-core` picks missing `(seg, block)` pairs:

### 3.1 Priority classes

```
1. INIT      — segment 0 (ftyp+moov), always highest priority before decode
2. URGENT    — segments in [playhead, playhead + 10 s]       (prevent stall)
3. WINDOW    — segments in [playhead + 10 s, playhead + 60 s] (buffer target)
4. SEEK      — pending seek target window (replaces URGENT/WINDOW on seek)
5. BACKFILL  — all other segments, rarest-first              (swarm health)
```

- **Rarest-first backfill** orders segments by ascending swarm holder count
  (ties randomized), running only when `URGENT`/`WINDOW` are healthy. It
  multiplies Host upload capacity by turning receivers into new seeders early.
- **Free-roam peers** center `URGENT`/`WINDOW` around *their own* playhead
  while continuing to serve the rest of the room.

### 3.2 Peer selection: capacity-proportional weighted random

For each candidate peer `i` holding `(seg, block)`, track EWMA throughput
`θ_i`, queue utilization `u_i = o_i / Q_i`, latency `(μ_i, σ_i)`, and
reliability `r_i ∈ [0, 1]`:

```
c_i = θ_i · max(0, 1 − u_i) · r_i
p_i = c_i / Σⱼ cⱼ
```

Weighted-random selection stripes blocks proportionally to available capacity
without greedy `argmax` herding.

### 3.3 Adaptive pipeline depth `Q` and SCTP pacing

```
Q_i = clamp( ceil( θ̂_i · RTT̂_i / 32 KiB ), 4, 64 )
```

`Q_i` keeps high-BDP paths saturated while bounding memory. Simultaneously, the
scheduler respects aggregate SCTP backpressure (`TransportBackpressure`, pausing
`Data`/`Bulk` sends above 256 KiB and resuming below 128 KiB) so upload/request
queues never bufferbloat `SYNC` on `control`.

### 3.4 Hedging, `CANCEL`, and endgame

- **Hedged requests**: if a requested block does not arrive within `μ_i + 3σ_i`,
  re-request from another candidate, send `CANCEL` to peer `i`, and decay
  `θ_i`/`r_i`.
- **Queue-only `CANCEL` on seek**: intersect existing requests with the new
  seek window (keep overlap), send `CANCEL` for out-of-window queued requests.
  Blocks already in flight are still verified and cached if they arrive.
- **Endgame mode**: when >95% of a stream (or the final 1–2 blocks of an urgent
  segment) remains, allow duplicate requests across candidates; first arrival
  wins and losers are cancelled.

---

## 4. Verification and upload policy

```
BLOCK arrives
  → check exact expected length for (seg, block)
  → BLAKE3("pw-block-v3" || bytes) + Merkle proof vs segment_root
      OK   → store in RAM hot tier immediately (servable now);
             when all blocks of seg present → PersistSegment to durable store
      FAIL → drop block; instant local ban of sender (exact, objective)
```

Upload policy allocates bandwidth proportionally to requester score `r_i`,
capped at `min(target_multiplier × bitrate, measured_upload × 0.8)` (default
1.5× bitrate). `LOW_UPLOAD` peers remain valid room members served normally at
lower upload priority.
