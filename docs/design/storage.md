---
tier: design
---

# Storage — RAM Hot Tier & Crash-Safe Segment Persistence

Peers cache segments to **play**, **re-upload**, and **resume** across
restarts. Native daemons can cache full films on disk (up to an LRU cap);
browser tabs operate as sliding-window partial seeders over RAM + OPFS.

---

## 1. Key principle: decouple live RAM serving from disk durability

Every incoming 32 KiB block is Merkle-verified in RAM against `segment_root`
before touching any storage tier:

- **Live serving (fast path)**: the moment a block verifies in RAM, `pw-core`
  may advertise it in `AVAILABILITY.frontier` and serve it to requesting peers
  immediately — **zero `fdatasync` latency in the swarm fan-out path**.
- **Durable persistence (crash-safe path)**: once all blocks of a segment
  verify in RAM, `pw-core` emits `Command::PersistSegment { stream, seg,
  bytes }`. The backend writes the segment, calls `fdatasync` / OPFS `flush`,
  appends to the checksummed index/journal, and `fdatasync`s the journal before
  emitting `Event::SegmentDurable`.

**Crash invariant**: the persisted on-disk index (`index.bin` / `index.journal`)
ONLY ever records a segment **after** its bytes are durable on disk. A power
failure loses at most the current unflushed batch in RAM and can never leave a
persisted bit set for missing bytes on restart.

---

## 2. Native storage (`pw-node`)

### 2.1 On-disk layout

```
$XDG_CACHE_HOME/peerwatch/<stream_id>/
├── manifest.head.cbor        # signed ManifestHead + subtitles
├── manifest.chunks/          # 32 KiB chunk files (~4 files for 2 h film)
├── segments.pack             # append-only segment data file
├── index.cbor                # seg_index -> (file_offset, byte_len, segment_root) + CRC
└── index.journal             # write-ahead append journal for newly durable segments
```

After `MANIFEST_FINAL`, an optional `<content_id>` alias allows instant
deduplication if the same finalized stream is re-shared later.

### 2.2 Range serving for `mpv`

Because every segment has an exact `byte_len`, the virtual linear fMP4 stream
is simply `seg[0] || seg[1] || ... || seg[N]`. `pw-node`'s localhost HTTP range
server maintains a prefix-sum offset table and maps any `Range: bytes=A-B`
request directly to `(seg_index, offset_in_seg)` in `O(log N)` time — serving
from the RAM hot tier first and `segments.pack` second.

### 2.3 Eviction and scrubbing

- Global LRU cap defaults to **20 GiB** (configurable, with stream pinning).
- Active host/playing streams are never evicted while in use.
- Startup replays `index.journal`, spot-checks random segments against
  `segment_root`, and runs a low-priority background scrub; any corrupted
  segment is invalidated and re-fetched.

---

## 3. Browser storage (`pw-wasm`)

Browsers provide two tiers inside the Worker:

1. **Hot tier (RAM LRU, ~128–256 MiB)**: holds verified frontier blocks and
   completed segments around `[playhead - 1 min, playhead + 3 min]` for zero-copy
   MSE appends and instant re-uploading.
2. **Warm tier (OPFS pack files)**: fixed-size ~64 MiB pack files
   (`pack-0000.opfs`, ...) plus a checksummed `index.cbor`, using synchronous
   `FileSystemSyncAccessHandle` inside the Worker. Budget is capped at ≤ 50% of
   `navigator.storage.estimate().quota` after requesting
   `navigator.storage.persist()`.

### 3.1 Eviction policy (browser)

1. **Rare-reserve (10% of budget)**: keep globally rarest segments (from the
   availability table) so browser peers strengthen swarm redundancy.
2. **Farthest-behind playhead** (already watched).
3. **Farthest-ahead beyond window** (over-fetched backfill).

Any `QuotaExceededError` or missing OPFS degrades cleanly to RAM-only streaming
(`L_STORE_QUOTA` / `L_PLATFORM_NO_OPFS`) without interrupting playback.

---

## 4. Storage adapter boundary

`pw-core` stays synchronous and zero-I/O, interacting exclusively via
`PersistSegment`, `LoadBlock`, `InvalidateSegment`, `FlushStore`, and `Evict`
commands ([core-api.md](../spec/core-api.md) §3). Platform shells implement the
actual native disk or OPFS pack backend behind this uniform interface.
