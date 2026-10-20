---
tier: design
---

# Storage — Where the Pieces Live

Peers hold pieces for three reasons: to **play** them, to **serve** them to
others, and to **resume** across restarts. The platforms differ enormously in
what storage they offer, so PeerWatch has two storage backends behind one
adapter boundary, with deliberately different *policies* per platform. The
protocol (bitfields, block proofs, requests, verification) is identical; only
the shelf differs.

The normative manifest/block formats are in
[protocol.md](../spec/protocol.md) §6. This document is the storage
policy layer.

---

## 1. Native (CLI / desktop): append-only file + sidecar metadata

### 1.1 Layout

```
$XDG_CACHE_HOME/peerwatch/
└── <stream_id>/
    ├── manifest.head.cbor        # signed head + chunk hashes
    ├── manifest.chunks/          # 32 KiB manifest chunks
    │   └── <chunk_index>.cbor
    ├── manifest.appends/         # live MANIFEST_APPEND records
    │   └── <version>.cbor
    ├── bitfield.bin              # persisted piece bitfield + checksum
    ├── bitfield.meta             # stream_id, manifest version, epoch
    └── video.partial             # append-only or pre-sized media file
```

After `MANIFEST_FINAL`, the cache may add a `content_id` symlink/alias for
content-addressed lookup. Before final, `stream_id` is the only stable key.

### 1.2 The media file

- For a **complete/remuxed/transmuxed** file with a known `total_size`, the
  peer may `ftruncate` `video.partial` to `total_size`. Sparse files then
  allocate blocks only where pieces are written.
- For a **live transcode**, `total_size` is unknown. Use an **append-only**
  file and grow it as segments are produced. Do not `ftruncate` to a guess;
  a wrong size wastes disk or truncates the stream. Pre-sizing after
  `MANIFEST_FINAL` is an optional optimization.
- Blocks are written at their byte offsets when possible; for live appends,
  sequential append is fine because piece indices are append-only. A random
  seek in a live stream may be a hole until the transcoder reaches it.

Properties that fall out:

- **Trivial range serving**: the mpv localhost server
  ([frontends.md](frontends.md)) maps HTTP `Range:` headers directly to file
  offsets. The OS page cache does readahead.
- **Resume**: restart the app, rejoin, load `bitfield.bin` and every piece
  you had is still valid. A rejoining Host of a remuxed/transcoded video
  keeps its whole output if the cache policy kept it.
- **Cheap correctness**: a power failure can lose at most recently-written
  blocks. See §1.3 for the ordering rule.

### 1.3 Crash consistency (normative ordering)

The bitfield and the media file are separate objects. A crash between them
must never leave a bit set for data that is not durable.

> **Verify first, then persist.** No unverified byte is ever written to a
> durable file. The block-assembly buffer in memory is the only place
> unverified bytes live, and it is bounded by `Q × 32 KiB` per peer
> ([protocol.md](../spec/protocol.md) §9). An earlier revision ordered the
> write before the check, which both contradicted the `put_verified`
> contract (§3) and gave an attacker a way to land arbitrary bytes on a
> victim's disk before any proof was evaluated.

Write path, per block:

1. Receive `BLOCK`; verify the Merkle proof against the trusted
   `piece_root` and check the length for its index
   ([protocol.md](../spec/protocol.md) §6.6–6.7). **Reject on failure.**
2. Place the verified block in the in-memory piece assembly buffer.
3. When all `block_count` blocks of a piece are present, write the piece
   to `video.partial` at its offset.
4. `fdatasync` the media file (batched per piece or per few pieces).
5. Append the piece index to the **write-ahead journal**
   (`bitfield.journal`) with a CRC.
6. `fdatasync` the journal. Only now may the piece appear in the in-memory
   bitfield and be advertised in `BITFIELD`.

Ordering rule, stated as an invariant: **a bit is set in an advertised
bitfield only after step 6 completed for that piece.** Steps 3–6 are
batched; the batch is the unit of durability.

On load:

1. Verify `bitfield.bin` checksum and its `stream_id`/manifest version.
2. Replay `bitfield.journal` on top of the snapshot; stop at the first
   bad CRC.
3. Cross-check a sample of pieces against the manifest roots.
4. Run a **background full scrub** of the rest; a piece that fails is
   cleared and re-fetched.
5. If both snapshot and journal are corrupt, discard the bitfield, keep
   the media file, and scrub from scratch (the bytes are still useful —
   they are re-verified, not re-downloaded).

A crash can therefore lose at most one un-journaled batch of pieces. It can
never leave a bit set for data that is not on disk, because the bit is set
*after* the fsync, not before.

### 1.4 Eviction and caps

- Per-stream data lives until the room ends, then it is kept (for reseed)
  until the **global cache cap** (default 20 GiB, configurable) pressures
  it out: LRU across `stream_id`s, with a “pin” flag to keep favorites as
  permanent seeds.
- A peer that completed a stream becomes a **complete peer** — a takeover
  candidate ([sync.md](sync.md) §6) and a long-term seed for late joiners.
- Do not evict pieces of the **current** stream while it is the active
  host/serving stream; otherwise a takeover candidate disappears.
- For a 100 GB stream on a 20 GiB cap, a native peer is a sliding-window
  archiver too, not a full archive. Advertise the real `storage_budget_mb`
  in `Caps`; do not claim “full” unless the file fits.

---

## 2. Browser: memory hot-tier + OPFS warm-tier

Browsers offer no general filesystem. Three storage primitives exist:

| Primitive | Capacity | Access | Notes |
|-----------|----------|--------|-------|
| JS memory (`ArrayBuffer`s) | whatever the tab survives | sync | gone on reload, fastest |
| IndexedDB | large, quota-bound | async only | clunky for 512 KiB blobs at high rate |
| **OPFS** (Origin Private File System) | large, quota-bound | **sync inside a Worker** | real files, atomic rename, the right tool |

Quota reality: `navigator.storage.estimate()` reports the origin's budget;
desktop Chrome/Edge typically allow gigabytes, Safari and mobile are
tighter. The storage layer reads the estimate at startup, calls
`navigator.storage.persist()` where available, and sizes itself to a
fraction of the quota (default ≤ 50 %, hard-capped). It must handle
`QuotaExceededError` by evicting and degrading to streaming-only; a quota
failure must not kill playback.

### 2.1 The two tiers

```
write path:  verified block ──► block assembly buffer
                                  │ piece complete
                                  ▼
                              HOT (memory LRU, cap ~256 MiB)
                                  │ spill when full
                                  ▼
                              WARM (OPFS pack files)
read path:   scheduler/player ──► HOT? hit : WARM? promote : fetch from swarm
```

- **Hot tier (~256 MiB)** covers the playback window with margin: at
  8 Mbps, ±2 min around the playhead ≈ 120 MB. The player appends from here,
  and uploads to peers requesting near-live pieces come from here.
- **Warm tier (OPFS)** holds everything else the peer decided to keep,
  enabling resume-after-reload and letting a browser tab keep *seeding*
  older parts of the film.
- OPFS sync handles exist only in Workers, one of the reasons the browser
  core runs in a Worker.

### 2.2 Pack files, not one file per piece

Do **not** create one OPFS file per 512 KiB piece. A 6 GB video is 12,288
files; a 100 GB video is 200,000. Directory metadata and file-open overhead
dominate, and eviction becomes a metadata storm.

Use **pack/extent files**:

```
<stream_id>/pack-0000.opfs
<stream_id>/pack-0001.opfs
<stream_id>/index.cbor
```

- Each pack holds a fixed number of pieces (e.g. 128 pieces = 64 MiB).
- `index.cbor` maps `piece → (pack, offset, length, piece_root)`.
- Writes go to a temporary extent and are atomically renamed; the index is
  updated after fsync/close.
- Eviction deletes whole packs when possible, or compacts a pack when only
  a few pieces are live.
- The index is checksummed and rebuilt by scanning packs if corrupt.

### 2.3 Eviction policy

When a tier is full, evict by **playhead distance**, with one exception:

1. **Rare-reserve (10 % of budget, pinned):** pieces that the swarm
   availability table says are globally rarest are kept regardless of
   distance. A browser tab dropping the swarm's only spare copy of a rare
   piece hurts everyone, so the reserve makes browser peers net contributors
   to redundancy, not just consumers.
2. **Farthest-behind first** (already-watched region): the viewer is
   unlikely to rewind past a few minutes.
3. **Farthest-ahead beyond the window** (over-fetched backfill).

The rare-reserve is a heuristic, not a guarantee: availability can be
manipulated by lying bitfields. It is a scheduling hint, never an integrity
or authority decision.

### 2.4 The honest asymmetry

A 2 h 8 Mbps film is ~7 GB. Native peers can be full archivers if the cache
cap allows; browser peers are **sliding-window partial seeders** (~1–2 GB
footprint):

```
kept: [playhead − 2 min … playhead + 5 min]  ∪  rare-reserve
```

Consequences, by design:

- Bitfields truthfully advertise what a browser peer holds, so the scheduler
  sees the sliding window and simply doesn't ask it for ancient pieces.
- Rarest-first backfill *prefers landing rare pieces on native peers*
  (availability-weighted: native peers announce larger budgets via `Caps`),
  so the swarm's long-term redundancy lives on real disks.
- A room of *only* browser peers still works: the Host holds everything,
  and the windows of the peers tile the region around the playhead, which
  is the only region being consumed. If the Host leaves, the swarm holds the
  watched region and the live frontier ([sync.md](sync.md) §6 applies as
  usual).

---

## 3. One adapter boundary, two backends

`pw-core` is synchronous and zero-I/O. It does not call storage directly; it
emits `Store` commands and receives `BlockStored`/`PieceLoaded` events. The
platform shell implements a **storage adapter**. A representative trait
(platform side, not core) is:

```rust
type BoxFuture<'a, T> = Pin<Box<dyn Future<Output = T> + 'a>>;

/// Block-granular, because the data path and the crash-consistency rule are
/// block-granular (§1.3). A piece-granular `put` cannot express "12 of 16
/// blocks are durable".
trait PieceStore: Send + Sync {
    fn has_piece(&self, piece: u32) -> bool;
    fn has_block(&self, piece: u32, block: u16) -> bool;
    fn bitfield(&self) -> Arc<Bitfield>;               // snapshot, not a borrow

    /// Read one block. Returns `None` if absent (a hole, or a partial piece).
    fn get_block(&self, piece: u32, block: u16) -> BoxFuture<'_, Result<Option<Bytes>>>;

    /// Persist one already-verified block. Idempotent: writing the same
    /// verified bytes twice is a no-op, never an error.
    fn put_block_verified(
        &self,
        piece: u32,
        block: u16,
        data: Bytes,
    ) -> BoxFuture<'_, Result<PutOutcome>>;

    /// Drop every block of a piece that failed a scrub.
    fn invalidate_piece(&self, piece: u32) -> BoxFuture<'_, Result<()>>;

    fn budget(&self) -> StoreBudget;                   // for capability advertisement
    fn evict(&self, policy: &EvictionPolicy) -> BoxFuture<'_, Result<()>>;
    fn flush(&self) -> BoxFuture<'_, Result<()>>;       // journal fsync barrier
}

enum PutOutcome {
    Stored,
    AlreadyPresent,
    PieceComplete { piece: u32 },   // caller may now set the bit + advertise
}
```

Why block-granular and why `PutOutcome`:

- The scheduler requests blocks, verifies blocks, and hedges blocks. A
  piece-granular store forces the adapter to reimplement piece assembly,
  which is core logic that must not be duplicated per platform.
- `PieceComplete` is the only signal that authorizes setting a bit in an
  advertised bitfield (§1.3 step 6). Making it an explicit return value
  stops an adapter from optimistically advertising a partially written
  piece.
- `flush` is the durability barrier the core uses before it tells the
  swarm it holds something. It maps to `fdatasync` on native and to an OPFS
  `close`/`sync` on the browser.
- `get_block` returning `Option` is what makes sparse files and OPFS pack
  holes representable without a second error type.

A convenience `put_piece_verified` may exist in a helper crate for tests,
but it is not part of the trait: production code goes through blocks.

Why boxed futures: `async fn` and `-> impl Future` in a trait are not
object-safe, so `Arc<dyn PieceStore>` does not compile with them. A boxed
future (or `async-trait`) keeps the trait dyn-compatible. Returning an
`Arc<Bitfield>` snapshot avoids borrowing across `await`.

Verification happens **before** `put_block_verified`: stores never hold
unverified bytes beyond the bounded block-assembly buffer (§1.3 step 1). A
bad block never reaches the store, and therefore never reaches a disk.

For the browser, the adapter is implemented in Rust/WASM and uses OPFS pack
files; for native, it uses the append-only file + journal. The core sees the
same commands/events either way.

---

## 4. Sizing cheat sheet

| Video | Total size | Native footprint | Browser footprint |
|-------|-----------:|-----------------:|------------------:|
| 2 h @ 2 Mbps (SD) | ~1.8 GB | full if cap allows (or evicted) | window ~45 MB hot + OPFS to budget |
| 2 h @ 8 Mbps (1080p) | ~7.2 GB | full if cap allows | window ~120 MB hot + OPFS |
| 2 h @ 20 Mbps (4K) | ~18 GB | full if cap allows | window ~300 MB hot (cap raised) + OPFS |

Bitfield persistence (`bitfield.bin` / OPFS index) is written on a 5 s
dirty-tick and on clean shutdown, never on the hot path. The bitfield always
carries a checksum and the `stream_id` it belongs to.
