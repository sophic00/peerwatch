# Storage — Where the Pieces Live

Peers hold pieces for three reasons: to **play** them, to **serve** them to
others, and to **resume** across restarts. The platforms differ enormously in
what storage they offer, so PeerWatch has two storage backends behind one
trait, with deliberately different *policies* per platform. The protocol
(bitfields, requests, verification) is identical, only the shelf differs.

---

## 1. Native (CLI / desktop): sparse file + sidecar metadata

### 1.1 Layout

```
$XDG_CACHE_HOME/peerwatch/
└── <content_id>/
    ├── manifest.json        # the manifest (segment table, hashes, codecs)
    ├── bitfield.bin         # persisted piece bitfield
    └── video.partial        # sparse file, pre-sized to total_size
```

### 1.2 The sparse file

On first sight of a manifest, the peer `ftruncate`s `video.partial` to
`total_size`. This creates a file that *logically* has the full size but
occupies **no disk blocks**: blocks are allocated only where pieces are
actually written (at their byte offsets). Properties that fall out for free:

- **Trivial range serving**: the mpv localhost server
  ([frontends.md](frontends.md)) maps HTTP `Range:` headers directly to file
  offsets, with no translation layer. The OS page cache does readahead.
- **Resume for free**: restart the app, rejoin the room, load
  `bitfield.bin` and every piece you had is still valid. A rejoining Host of a
  *remuxed/transcoded* video keeps its whole output too (the pipeline's temp
  file *is* the partial file).
- **Cheap correctness**: a power failure can lose at most recently-written
  blocks. Anything in the bitfield was fsync-batched, and on load the
  bitfield is cross-checked against the manifest hashes on a sampled basis.

### 1.3 Eviction and caps

- Per-room data lives until the room ends, then it's kept (for reseed) until
  the **global cache cap** (default 20 GiB, configurable) pressures it out:
  LRU across `content_id`s, with a "pin" flag to keep favorites as permanent
  seeds.
- A peer that completed a video becomes a **complete peer** — a takeover
  candidate ([sync.md](sync.md) §6) and a long-term seed for late joiners.

---

## 2. Browser: memory hot-tier + OPFS warm-tier

Browsers offer no filesystem. Three storage primitives exist, with different
shapes:

| Primitive | Capacity | Access | Notes |
|-----------|----------|--------|-------|
| JS memory (`ArrayBuffer`s) | whatever the tab survives | sync | gone on reload, fastest |
| IndexedDB | large, quota-bound | async only | clunky for 512 KiB blobs at high rate |
| **OPFS** (Origin Private File System) | large, quota-bound | **sync inside a Worker** | real files, atomic rename, the right tool |

Quota reality: `navigator.storage.estimate()` reports the origin's budget,
typically gigabytes on Chrome/Edge desktop, tighter on Safari and mobile.
The storage layer reads the estimate at startup and sizes itself to a
fraction of it (default ≤ 50 %, hard-capped).

### 2.1 The two tiers

```
write path:  verified piece ──► HOT (memory LRU, cap ~256 MiB)
                                    │ spill when full
                                    ▼
                              WARM (OPFS files: <content_id>/<piece_idx>,
                                    tmp + atomic rename)
read path:   scheduler/player ──► HOT? hit : WARM? promote : fetch from swarm
```

- **Hot tier (~256 MiB)** covers the playback window with margin: at 8 Mbps,
  ±2 min around the playhead ≈ 120 MB. The player appends from here, and
  uploads to peers requesting near-live pieces come from here.
- **Warm tier (OPFS)** holds everything else the peer decided to keep,
  enabling resume-after-reload and letting a browser tab keep *seeding*
  older parts of the film. OPFS sync handles exist only in Workers, one of
  the reasons `pw-core`'s storage runs in a Web Worker, not on the main
  thread.

### 2.2 Eviction policy

When a tier is full, evict by **playhead distance**, with one exception:

1. **Rare-reserve (10 % of budget, pinned):** pieces that the swarm
   availability table says are globally rarest are kept regardless of
   distance. A browser tab dropping the swarm's only spare copy of a rare
   piece hurts everyone, so the reserve makes browser peers net contributors to
   redundancy, not just consumers.
2. **Farthest-behind first** (already-watched region), the viewer is
   unlikely to rewind past a few minutes.
3. **Farthest-ahead beyond the window** (over-fetched backfill).

### 2.3 The honest asymmetry

A 2 h 8 Mbps film is ~7 GB. Native peers can be full archivers, browser
peers are **sliding-window partial seeders** (~1–2 GB footprint):

```
kept: [playhead − 2 min … playhead + 5 min]  ∪  rare-reserve
```

Consequences, by design:

- Bitfields truthfully advertise what a browser peer holds, so the scheduler
  sees the sliding window and simply doesn't ask it for ancient pieces.
- Rarest-first backfill *prefers landing rare pieces on native peers*
  (availability-weighted: native peers announce larger budgets via
  capability flags), so the swarm's long-term redundancy lives on real
  disks.
- A room of *only* browser peers still works: the Host holds everything,
  and the windows of the peers tile the region around the playhead, which
  is the only region being consumed. If the Host leaves, the swarm holds the
  watched region and the live frontier ([sync.md](sync.md) §6 applies as
  usual).

---

## 3. One trait, two backends

```rust
trait PieceStore {
    fn has(&self, piece: u32) -> bool;
    fn get(&self, piece: u32) -> impl Future<Output = Option<Bytes>>;
    fn put_verified(&self, piece: u32, data: Bytes) -> impl Future<Output = ()>;
    fn bitfield(&self) -> &Bitfield;
    fn budget(&self) -> StoreBudget;      // for capability advertisement
    fn evict(&self) -> impl Future<Output = ()>;  // policy tick
}
```

`pw-core` only ever talks to this trait. The native backend is the sparse
file, and the browser backend is the two-tier memory+OPFS store. Verification
always happens *before* `put_verified`: stores never hold unverified bytes
beyond the piece-assembly buffer.

---

## 4. Sizing cheat sheet

| Video | Total size | Native footprint | Browser footprint |
|-------|-----------:|-----------------:|------------------:|
| 2 h @ 2 Mbps (SD) | ~1.8 GB | full (or evicted per cap) | window ~45 MB hot + OPFS to budget |
| 2 h @ 8 Mbps (1080p) | ~7.2 GB | full | window ~120 MB hot + OPFS |
| 2 h @ 20 Mbps (4K) | ~18 GB | full | window ~300 MB hot (cap raised) + OPFS |

Bitfield persistence (`bitfield.bin` / an OPFS manifest mirror) is written
on a 5 s dirty-tick and on clean shutdown, never on the hot path.
