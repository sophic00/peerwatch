---
tier: design
---

# Distribution — How the Video Is Chunked and Shared

This document covers the data plane: the manifest (the “map” of a video),
how bytes are chopped into pieces and blocks, how peers advertise what they
have, and the scheduler that decides *which block to fetch from whom, when*,
including the mathematics behind those decisions.

The normative manifest and block-proof formats are in
[protocol.md](../spec/protocol.md) §6. This document is the
explanatory and scheduling layer.

---

## 1. The three levels (recap)

The Segment / Piece / Block granularity is defined once in
[architecture.md §1](architecture.md#1-terminology) and
[protocol.md](../spec/protocol.md) §6.1. That table is the single
source of truth; this document only adds distribution-specific context:

- 16 blocks = 1 piece. A 4 s segment of 8 Mbps video = 4 MB = 8 pieces =
  128 blocks. The last piece may have fewer than 16 blocks, and the last
  block may be shorter than 32 KiB; the manifest’s piece/block counts define
  the tail.
- Pieces and segments are aligned only through the segment table. Pieces
  don't care about keyframes; segments don't care about 512 KiB boundaries.
- The block is 32 KiB. A 32 KiB block plus a 4-hash Merkle proof fits
  inside the 64 KiB DataChannel limit with room to spare
  ([design-log.md](design-log.md) D9).

---

## 2. The manifest

Everything a peer needs to fetch and verify a stream is committed by the
Host, but it is **not** a single 400 KB–6 MB message. It is a signed head
plus chunked piece roots and segment entries.

```
StreamParams = {
  profile: FMP4 | WEBM,
  codecs: "avc1.640028,mp4a.40.2",   // actual emitted codec string
  piece_length: 524288,              // 512 KiB
  block_length: 32768,               // 32 KiB
  target_segment_ms: 4000,
}

ManifestHead = {
  stream_id, version, manifest_hash,
  params,
  total_pieces, total_blocks,
  total_bytes: Option<u64>,          // unknown for live transcode
  duration_ms: Option<u64>,          // unknown for live transcode
  init_segment: SegmentRef,
  segment_count, chunk_count,
  chunk_hashes: [Hash32],
}

ManifestChunk = {
  stream_id, version, chunk_index,
  piece_start,
  piece_roots: [Hash32],             // ≤ 512
  segments: [SegmentEntry],          // ≤ 256
}

SegmentEntry = {
  t_start_ms, t_end_ms,
  piece_start, piece_end,            // inclusive
  block_start, block_end,            // inclusive
}
```

- **`piece_roots` is the root of data integrity.** Each piece root is a
  BLAKE3 Merkle root over that piece’s block hashes. Any block can be
  verified independently against the root, and a bad block is attributable
  to the sender before the rest of the piece arrives.
- **The segment table is the seek index.** Seeking to time T:
  binary-search the table → the segment containing T → its piece range →
  prioritize those pieces → decode from the segment start (a keyframe).
- **Size is chunked.** For a 6 GB video (12,288 pieces) the roots are
  ~400 KB; for 100 GB (200,000 pieces) ~6.4 MB. Both are transferred as
  32 KiB `MANIFEST_CHUNK` messages on the `bulk` channel, with the head
  committing to every chunk hash. A late joiner fetches only the chunks it
  needs, not the whole manifest.
- **Stable identity**: `stream_id` is random at proposal and never changes.
  `content_id` is assigned only by `MANIFEST_FINAL`. All data-plane
  messages use `stream_id`.
- **Incremental manifests**: during a live transcode, `MANIFEST_APPEND`
  adds new piece roots and segment entries. It is signed by the Host,
  carries `(term, seq, from_version → to_version)`, and is accepted only in
  order. A peer that falls behind requests the head and missing chunks.
  Deriving `content_id` from a growing manifest would invalidate every
  in-flight message and storage key on each append; `stream_id` exists to
  decouple them ([design-log.md](design-log.md) D11).

---

## 3. Advertising: periodic full bitfields with Roaring Compression

Every peer broadcasts its **entire piece bitfield** on the control channel
every ~1 second (same philosophy as v1: self-correcting, a lost update is
repaired by the next broadcast, no per-piece HAVE chatter).

To minimize bandwidth, bitfields are compressed using **Roaring Bitmaps**
([roaring-compression.md](../spec/roaring-compression.md)):

- A raw bitfield for a 6 GB video (12,288 pieces) is `N/8` = 1.5 KB. Under
  run-optimized Roaring, a complete host/seeder compresses to ~15 B, a
  fresh joiner to ~8 B, and a browser sliding-window peer (contiguous
  window + scattered rare-reserve) to ~470 B. Full sizing is in
  [roaring-compression.md](../spec/roaring-compression.md).
- Receivers update (a) the sender's bitfield (for “who has piece i?”) and
  (b) the **availability table**: `availability[i] = # of peers holding
  piece i` (the input to rarest-first).
- During a live transcode the Host's bitfield grows in real time; everyone
  sees the frontier advance.
- A bitfield is a **scheduling hint**, not proof of possession. A peer can
  lie about availability; the scheduler's reliability score and block
  proofs handle it. Takeover eligibility never relies on a bitfield.

---

## 4. The scheduler

The scheduler answers, every few hundred milliseconds per peer: *which
missing blocks should I request, from which peer, in what order?*

### 4.1 Priority classes (what to fetch)

```
1. URGENT    — pieces in [playhead, playhead + 10 s]          (stall imminent)
2. WINDOW    — pieces in [playhead + 10 s, playhead + 60 s]   (buffer target)
3. SEEK      — pieces in a pending seek target window         (on seek: replaces 1–2)
4. BACKFILL  — everything else, rarest-first                  (swarm health)
```

- **Playback window first** guarantees the only pieces that can stall
  playback are fetched ahead of need. Buffer target: 60 s.
- **Rarest-first backfill** orders remaining pieces by ascending
  `availability[i]` (ties: random). Why: the swarm's weak point is always
  the piece with fewest holders. If its holders leave, it's gone.
  Rarest-first equalizes redundancy and, crucially, **maximizes fan-out
  from the Host**: while a piece exists only at the Host, room-wide
  replication speed for it is `host_upload / K_requesters`; the sooner a
  second holder exists, the sooner capacity for that piece doubles. This is
  what lets playback survive the Host leaving ([sync.md](sync.md) §6).
- **Backfill is throttled.** It must not compete with URGENT/WINDOW or
  saturate the uplink. It runs only when the window is healthy and there is
  spare capacity.
- **Free-roam peers** ([sync.md](sync.md) §5) run the same classes around
  *their own* playhead instead of the room's.

### 4.2 From whom: capacity-proportional selection

When a block is available from a candidate set P of peers, choose to
maximize aggregate throughput and minimize stalls, without herding.

Per candidate i, maintain (EWMA-smoothed): measured throughput `θ_i`
(from recent block arrivals), utilization `u_i = o_i / Q_i` (outstanding
blocks over its BDP window), latency stats `(μ_i, σ_i)`, and reliability
`r_i ∈ [0,1]` (decays on timeouts/violations, recovers slowly). Estimated
free capacity:

```
c_i = θ_i · max(0, 1 − u_i) · r_i
```

**Selection rule: pick peer i with probability `p_i = c_i / Σⱼ cⱼ`**
(weighted random), striping consecutive blocks across picks.

`θ_i` is already measured *while* `o_i` blocks are outstanding, so a formula
like `θ_i / (1 + o_i)` would double-count queueing and mix units. The
utilization form is dimensionally consistent and keeps the same intent.

*Why this rule — the divisible-load argument.* To fetch W bits from sources
with capacities `c_i`, the makespan-optimal split has every source finish
simultaneously: `w_i = c_i·T`, `Σw_i = W` → `T = W / Σc_i`. Any other split
idles capacity. Capacity-proportional assignment *is* that allocation,
achieved statistically, with no coordination.

*Why weighted random and not argmax(c_i).* Greedy maximizes the instantaneous
choice but **herds**: every requester independently picks the same “best”
peer (usually the Host), its queue inflates, everyone's estimates go stale
together, then the herd stampedes elsewhere — classic oscillation. Weighted
random spreads load proportional to capacity using only local, stale-tolerant
information, and smoothly avoids punished peers as `r_i` decays. (Optional
refinement: *power of two choices* — sample two candidates, take the better —
which keeps herd-freedom while exponentially improving tail behavior over
uniform random.)

*What it beats:* uniform random/round-robin (a 1 Mbps peer gets as many
requests as a 100 Mbps peer, mean block time dominated by the slow picks),
and BitTorrent-style tit-for-tat (good incentives, no latency model).

### 4.3 How many requests in flight: the BDP rule

Throughput on a path is capped by `in_flight_bytes / RTT`. To saturate a
link, in-flight bytes must cover the **bandwidth–delay product**:

```
Q = clamp( ceil( θ̂_i · RTT̂_i / block_size ), 4, 64 )
```

- LAN (1 Gbps × 0.5 ms): BDP ≈ 62 KB → Q = 4 (minimum).
- Broadband (20 Mbps × 30 ms): BDP ≈ 75 KB / 32 KiB ≈ 3 → raised to 4.
- Fast WAN (100 Mbps × 100 ms): BDP ≈ 1.25 MB / 32 KiB ≈ 40.
- A fixed Q=8 would silently cap that last path at ~20 Mbps.

θ̂ and RTT̂ are re-estimated continuously, so Q adapts as conditions change.
**Q is not the only queue.** WebRTC DataChannels have their own SCTP
congestion control and send buffer. The scheduler MUST also respect
`bufferedAmount` / `bufferedAmountLowThreshold` (native: the `webrtc-rs`
equivalent) and stop queueing when the transport is backed up. An app-level
queue that ignores SCTP backpressure will only add latency and memory.

### 4.4 Tail control: hedged re-requests

Block arrival times have long tails (a peer stalls, a path congests).
If a requested block hasn't arrived within `μ_i + 3σ_i`:

1. re-request it from the next-best candidate,
2. send `CANCEL` for the original request,
3. decay `θ_i` and `r_i`.

First arrival wins, the loser is cancelled. Hedged requests are the standard
“Tail at Scale” result: >50 % tail-latency reduction for a few percent of
extra traffic, here one 32 KiB duplicate block at worst.

### 4.5 CANCEL: the bandwidth accounting

A seek invalidates queued requests. CANCEL's exact semantics and cost:

- A sender's requests exist in three states: **queued** (not yet
  transmitted), **in flight** (≤ BDP), **delivered**.
- **CANCEL kills only queued requests.** In-flight blocks complete and are
  *stored anyway*: they are valid blocks of the same stream (useful if the
  viewer seeks back), so they're deprioritized, not wasted.
- Waste *with* CANCEL per connection: `W = BDP = θ·RTT`, ~31 KB at
  5 Mbps×50 ms, 1.25 MB at 100 Mbps×100 ms (worst case).
- Waste *without* CANCEL: the sender also drains its queue (32–64 blocks =
  1–2 MB per peer at 32 KiB → 10–20 MB across a 10-peer mesh *per seek*),
  and the queue drain delays the new window by `queued_bits / θ` — 4–8 s
  of extra post-seek stall on a 2 Mbps path.
- Refinement: on seek, **intersect, don't purge**. Requests whose pieces
  fall inside the *new* playback window are re-prioritized, not cancelled.

CANCEL is one small batch message (at most 64 entries). It saves megabytes
and seconds of stall.

### 4.6 Endgame mode

When >95 % of pieces are complete (or a viewer is one piece from a seek
target), the scheduler may request the *same* block from multiple candidates
concurrently (first arrival wins, losers cancelled). Cost: bounded duplicate
blocks; benefit: completion time stops depending on the single slowest
source.

---

## 5. Verification and the store path

```
block arrives
  → BLAKE3(block bytes)
  → verify Merkle proof against trusted piece_root
      yes → commit block; if all blocks in piece are present, piece is complete
      no  → drop block; ban the exact sender (local, objective)
```

A peer only ever **uploads verified blocks**, so poisoning cannot transit
through honest peers. Verified pieces feed two consumers: the local player
(via the platform's playback path, [frontends.md](frontends.md)) and the
upload side of the scheduler (serving others' requests).

Hashing a whole 512 KiB piece after assembling blocks from possibly
*different* senders cannot attribute blame, and a malicious peer could
contribute one bad block to frame an honest one. Per-block Merkle proofs make
blame exact ([design-log.md](design-log.md) D10).

Upload policy: the scheduler answers requests from any non-banned peer, but
allocates its upload budget proportionally to requesters' scores
(`r_i`-weighted), so abusive or freeloading peers get served last and
slowest. The default target is ≈ 1.5× video bitrate, but the effective cap
is `min(1.5× bitrate, measured_upload × 0.8)`. Browser peers on metered
connections are legitimate low-uploaders (`LOW_UPLOAD`); 0 upload is allowed
but visibly marked.

---

## 6. Putting numbers on it (pointer)

Worked scenarios (“how fast does a block reach a peer” under lone-host,
shared-host, swarm-striping, rare-piece, and lossy-link conditions, plus the
full CPU/time budget) live in [performance.md](performance.md). The
headline results that shaped this design:

- Host uplink is the early bottleneck → rarest-first fan-out + 60 s buffer.
- Lossy/high-RTT paths collapse (∝ 1/√loss) → stripe across peers, hedge tails.
- Queue depth must track BDP and the transport's `bufferedAmount` → adaptive
  Q, not a fixed number and not an unbounded app queue.
- Block-level proofs cost ~4 × 32 B per 32 KiB block (~0.4 %) and eliminate
  whole-piece blame ambiguity; that is a good trade.
