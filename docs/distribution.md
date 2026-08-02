# Distribution — How the Video Is Chunked and Shared

This document covers the data plane: the manifest (the "map" of a video),
how bytes are chopped into pieces and blocks, how peers advertise what they
have, and the scheduler that decides *which block to fetch from whom, when*,
including the mathematics behind those decisions.

---

## 1. The three levels (recap)

The Segment / Piece / Block granularity is defined once in
[architecture.md §1](architecture.md#1-terminology) (sizes, roles, alignment
rules). That table is the single source of truth, this document only adds
distribution-specific context on top of it:

- 8 blocks = 1 piece. A 4 s segment of 8 Mbps video = 4 MB = 8 pieces = 64
  blocks.
- Pieces and segments are aligned only through the segment table. Pieces
  don't care about keyframes, segments don't care about 512 KiB boundaries.

---

## 2. The manifest

Everything a peer needs to fetch and verify a video, created by the Host
when the video is proposed:

```
manifest = {
  version: 2,
  stream_profile: FMP4 | WEBM,
  codecs: "avc1.640028,mp4a.40.2",   // exact MSE/mpv codec string
  title, duration_ms, total_size,
  piece_length: 524288,              // 512 KiB
  piece_hashes: [sha256 × N],        // one per piece — the trust anchor
  init_segment: { piece_start, piece_end },
  segments: [ { t_start, t_end, piece_start, piece_end } × M ],
}
content_id = blake3(serialize(manifest))
```

- **`piece_hashes` is the root of data integrity.** Any byte range in the
  file can be verified by hashing its piece and comparing. A peer sending
  garbage is detected deterministically ([security.md](security.md)).
- **The segment table is the seek index.** Seeking to time T: binary-search
  the table → the segment containing T → its piece range → prioritize those
  pieces → decode from the segment start (a keyframe). Exactly how
  DASH/HLS seek, ported to P2P.
- **Size**: ~32 bytes of hash per piece → a 6 GB video (12 288 pieces) has a
  ~400 KB manifest. Sent once per peer on join/activation, cheap.
- **Incremental manifests**: during a live transcode, the segment table and
  hash list grow via signed `MANIFEST_APPEND` messages from the Host
  ([media-pipeline.md](media-pipeline.md) §5). The content ID of a growing
  manifest commits to its *final* form only when the Host signs
  `MANIFEST_FINAL`, until then, appends are verified against the Host's key.

---

## 3. Advertising: periodic full bitfields with Roaring Compression

Every peer broadcasts its **entire piece bitfield** on the control channel
every ~1 second (same philosophy as v1: self-correcting, a lost update is
repaired by the next broadcast, no per-piece HAVE chatter).

To minimize bandwidth, bitfields are compressed using **Roaring Bitmaps** ([roaring-compression.md](roaring-compression.md)):

- **Uncompressed vs Compressed Size**: a raw bitfield for a 6 GB video (12,288 pieces) is `N/8` = 1.5 KB. Under Roaring (run-optimized), a complete host/seeder compresses to ~15 B (99.0% reduction), a fresh joiner to ~8 B, and a browser sliding-window peer (contiguous window + scattered rare-reserve) to ~470 B. Full sizing and the run-optimization requirement are in [roaring-compression.md](roaring-compression.md).
- Receivers update (a) the sender's bitfield (for "who has piece i?") and
  (b) the **availability table**: `availability[i] = # of peers holding piece i` (the input to rarest-first).
- During a live transcode the Host's bitfield grows in real time, everyone
  sees the frontier advance.

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
  the piece with fewest holders. If its holders leave, it's gone. Rarest-
  first equalizes redundancy and, crucially, **maximizes fan-out from the
  Host**: while a piece exists only at the Host, room-wide replication speed
  for it is `host_upload / K_requesters`, the sooner a second holder exists,
  the sooner capacity for that piece doubles. This is what lets playback
  survive the Host leaving ([sync.md](sync.md) §6).
- **Free-roam peers** ([sync.md](sync.md) §5) run the same classes around
  *their own* playhead instead of the room's.

### 4.2 From whom: capacity-proportional selection

When a block is available from a candidate set P of peers, choose to
maximize aggregate throughput and minimize stalls, without herding.

Per candidate i, maintain (EWMA-smoothed): measured throughput `θ_i`
(from recent block arrivals), outstanding blocks `o_i`, latency stats
`(μ_i, σ_i)`, and reliability `r_i ∈ [0,1]` (decays on timeouts/violations,
recovers slowly). Estimated free capacity:

```
c_i = θ_i / (1 + o_i) · r_i
```

**Selection rule: pick peer i with probability `p_i = c_i / Σⱼ cⱼ`**
(weighted random), striping consecutive blocks across picks.

*Why this rule — the divisible-load argument.* To fetch W bits from sources
with capacities `c_i`, the makespan-optimal split has every source finish
simultaneously: `w_i = c_i·T`, `Σw_i = W` → `T = W / Σc_i`. Any other split
idles capacity. Capacity-proportional assignment *is* that allocation,
achieved statistically, with no coordination.

*Why weighted random and not argmax(c_i).* Greedy maximizes the instantaneous
choice but **herds**: every requester independently picks the same "best"
peer (usually the Host), its queue inflates, everyone's estimates go stale
together, then the herd stampedes elsewhere, classic oscillation. Weighted
random spreads load proportional to capacity using only local, stale-tolerant
information, and smoothly avoids punished peers as `r_i` decays. (Optional
refinement: *power of two choices* (sample two candidates, take the better),
   which keeps herd-freedom while exponentially improving tail behavior over
   uniform random.)

*What it beats:* uniform random/round-robin (a 1 Mbps peer gets as many
requests as a 100 Mbps peer, mean block time dominated by the slow picks),
and BitTorrent-style tit-for-tat (good incentives, no latency model).

### 4.3 How many requests in flight: the BDP rule

Throughput on a path is capped by `in_flight_bytes / RTT`. To saturate a
link, in-flight bytes must cover the **bandwidth–delay product**:

```
Q = clamp( ⌈θ̂_i · RTT̂_i / block_size⌉ , 4, 64 )
```

- LAN (1 Gbps × 0.5 ms): Q = 4 (minimum).
- Broadband (20 Mbps × 30 ms): Q ≈ 2 → raised to floor of 4.
- Fast WAN (100 Mbps × 100 ms): Q ≈ 20 (1.25 MB in flight).
- A fixed Q=8 would silently cap that last path at 41 Mbps.

θ̂ and RTT̂ are re-estimated continuously, so Q adapts as conditions change.

### 4.4 Tail control: hedged re-requests

Block arrival times have long tails (a peer stalls, a path congests).
If a requested block hasn't arrived within `μ_i + 3σ_i`:

1. re-request it from the next-best candidate,
2. send `CANCEL` for the original request,
3. decay `θ_i` and `r_i`.

First arrival wins, the loser is cancelled. Hedged requests are the standard
"Tail at Scale" result: >50 % tail-latency reduction for a few percent of
extra traffic, here one 64 KB duplicate block at worst.

### 4.5 CANCEL: the bandwidth accounting

A seek invalidates queued requests. CANCEL's exact semantics and cost:

- A sender's requests exist in three states: **queued** (not yet
  transmitted), **in flight** (≤ BDP), **delivered**.
- **CANCEL kills only queued requests.** In-flight blocks complete and are
  *stored anyway*: they are valid pieces of the same video (useful if the
  viewer seeks back), so they're deprioritized, not wasted.
- Waste *with* CANCEL per connection: `W = BDP = θ·RTT`, 31 KB at
  5 Mbps×50 ms, 1.25 MB at 100 Mbps×100 ms (worst case).
- Waste *without* CANCEL: the sender also drains its queue (32–64 blocks =
  2–4 MB per peer → 20–40 MB across a 10-peer mesh *per seek*), and the
  queue drain delays the new window by `queued_bits / θ`, up to 8 s of
  extra post-seek stall on a 2 Mbps path.
- Refinement: on seek, **intersect, don't purge**. Requests whose pieces
  fall inside the *new* playback window are re-prioritized, not cancelled.

CANCEL is one 32-byte message that saves megabytes and seconds of stall.

### 4.6 Endgame mode

When >95 % of pieces are complete (or a viewer is one piece from a seek
target), the scheduler may request the *same* block from multiple candidates
concurrently (first arrival wins, losers cancelled). Cost: bounded duplicate
blocks, benefit: completion time stops depending on the single slowest
source.

---

## 5. Verification and the store path

```
block arrives → assembled into piece buffer
piece complete → SHA-256(piece) == manifest.piece_hashes[i]?
   yes → commit to store → set bitfield bit → eligible for upload
   no  → drop piece; penalize sender (instant local ban — see security.md)
```

A peer only ever **uploads verified pieces**, so poisoning cannot transit
through honest peers. Verified pieces feed two consumers: the local player
(via the platform's playback path, [frontends.md](frontends.md)) and the
upload side of the scheduler (serving others' requests).

Upload policy: the scheduler answers requests from any non-banned peer, but
allocates its upload budget proportionally to requesters' scores
(`r_i`-weighted), so abusive or freeloading peers get served last and slowest.
Default upload cap ≈ 1.5× video bitrate (configurable, 0 = leech mode is
allowed but visibly marked, since browser peers on metered connections are
legitimate low-uploaders).

---

## 6. Putting numbers on it (pointer)

Worked scenarios ("how fast does a piece reach a peer" under lone-host,
shared-host, swarm-striping, rare-piece, and lossy-link conditions, plus the
full CPU/time budget) live in [performance.md](performance.md). The
headline results that shaped this design:

- Host uplink is the early bottleneck → rarest-first fan-out + 60 s buffer.
- Lossy/high-RTT paths collapse (∝ 1/√loss) → stripe across peers, hedge tails.
- Queue depth must track BDP → adaptive Q, not fixed.
